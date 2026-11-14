// Package resume drives the whole application through what a long index
// meets: model servers that are down, refuse or cut an answer short, and a
// process killed midway and started again on the same data.
package resume

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"deepcode/internal/app"
	"deepcode/internal/platform/storage"
)

// model is an OpenAI-compatible embeddings server: one vector per text, or
// every request answered with status and refusal, or one request held once a
// number of batches were answered.
type model struct {
	mu       sync.Mutex
	status   int            // 0 answers; else every request gets it
	refusal  string         // the body sent with status
	hold     int            // with held set: the batch after this many answered ones is held
	held     chan struct{}  // closed once a request is held
	release  chan struct{}  // closed to let the held request go
	answered map[string]int // how often each text was answered
	requests int
}

func newModel() *model { return &model{answered: map[string]int{}} }

// fail answers every request with status and body from now on, a plain
// refusal for body ""; status 0 answers them again.
func (m *model) fail(status int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status, m.refusal = status, cmp.Or(body, `{"error":{"message":"fake model says no"}}`)
}

// holdAfter holds the request that comes after n more answered batches,
// until release is closed; held is closed when it comes.
func (m *model) holdAfter(n int) (held, release chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hold, m.held, m.release = n, make(chan struct{}), make(chan struct{})
	return m.held, m.release
}

func (m *model) counts() (texts, requests int, twice []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for s, n := range m.answered {
		if n > 1 {
			twice = append(twice, s)
		}
	}
	return len(m.answered), m.requests, twice
}

func (m *model) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Input []string `json:"input"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	m.mu.Lock()
	m.requests++
	if m.status != 0 {
		status, refusal := m.status, m.refusal
		m.mu.Unlock()
		w.Header().Set("Retry-After-Ms", "1") // the SDK's own retries come at once
		http.Error(w, refusal, status)
		return
	}
	if m.held != nil && m.hold == 0 {
		held, release := m.held, m.release
		m.held = nil
		m.mu.Unlock()
		close(held)
		<-release
		return
	}
	m.hold--
	data := make([]map[string]any, len(body.Input))
	for i, s := range body.Input {
		m.answered[s]++
		data[i] = map[string]any{"index": i, "embedding": []float32{float32(len(s)), float32(i + 1), 1}}
	}
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": data})
}

// remote is a git repository of files enough functions, of more lines than
// the chunker merges, for about ten batches of embeddings. Its GitHub URL
// leads to it.
func remote(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "uploadpack.allowAnySHA1InWant", "true")
	for f := range 100 {
		var b strings.Builder
		fmt.Fprintf(&b, "package p%d\n", f)
		for fn := range 3 {
			fmt.Fprintf(&b, "\n// F%d sums some numbers.\nfunc F%d() int {\n\tx := 0\n", fn, fn)
			for line := range 70 {
				fmt.Fprintf(&b, "\tx += %d\n", f*1000+fn*100+line)
			}
			b.WriteString("\treturn x\n}\n")
		}
		os.MkdirAll(filepath.Join(dir, fmt.Sprintf("p%d", f)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("p%d", f), "f.go"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "files")
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+p+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/acme/demo.git")
	t.Setenv("GIT_CONFIG_KEY_1", "gc.auto")
	t.Setenv("GIT_CONFIG_VALUE_1", "0")
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "core.autocrlf=false", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// githubAPI answers like GitHub's REST API for acme/demo.
var githubAPI = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	if r.RequestURI != "/repos/acme/demo" {
		http.NotFound(w, r)
		return
	}
	fmt.Fprint(w, `{"full_name":"acme/demo","private":false,"description":"Demo","stargazers_count":1,"size":12,"default_branch":"main"}`)
})

// start runs the application on the data folder data, embedding with the
// server at embedURL, with workers workers, until stop. It returns the API's
// address and the database.
func start(t *testing.T, data, embedURL string, workers int) (base string, db *sql.DB, stop func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	db, err := storage.Open(ctx, filepath.Join(data, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	spa := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "spa") })
	a, err := app.New(ctx, app.Config{
		DataDir:  data,
		EmbedURL: embedURL + "/v1", EmbedModel: "fake-embed",
		LLMTimeout: time.Minute, Workers: workers, JobTimeout: time.Minute, CacheMB: 1,
		MaxRepoMB: 10, MaxFiles: 1000, MaxFileKB: 1024, MaxIndexMB: 10,
		GitProtocols: "file", AdminPassword: "admin-password", SessionTTL: time.Hour,
	}, db, spa, true)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(stopped)
	}()
	srv := httptest.NewServer(a.Handler)
	return srv.URL, db, func() {
		cancel()
		<-stopped
		srv.Close()
		db.Close()
	}
}

// client signs in as the administrator.
func client(t *testing.T, base string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code := call(t, c, http.MethodPost, base+"/api/auth/login", `{"username":"admin","password":"admin-password"}`); code != http.StatusOK {
		t.Fatalf("sign in: %d", code)
	}
	return c
}

// importDemo points GitHub at api and imports acme/demo, returning its public id.
func importDemo(t *testing.T, base, api string) string {
	t.Helper()
	c := client(t, base)
	if code := call(t, c, http.MethodPatch, base+"/api/admin/servers/1", fmt.Sprintf(`{"apiUrl":%q}`, api)); code != http.StatusOK {
		t.Fatalf("point GitHub at the fake API: %d", code)
	}
	var repo struct {
		ID string `json:"id"`
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/api/repos", strings.NewReader(`{"url":"https://github.com/acme/demo"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if json.NewDecoder(res.Body).Decode(&repo); res.StatusCode != http.StatusAccepted {
		t.Fatalf("import: %d", res.StatusCode)
	}
	return repo.ID
}

func call(t *testing.T, c *http.Client, method, url, body string) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode
}

// indexJob is the repository's index job as the queue keeps it.
type indexJob struct {
	Status, Error    string
	Attempts         int
	Retries          int
	RunsAgainSeconds int
}

// waitJob waits until the index job satisfies done, and returns it.
func waitJob(t *testing.T, db *sql.DB, what string, done func(indexJob) bool) indexJob {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		var j indexJob
		err := db.QueryRow(`SELECT status, error, attempts, retries, unixepoch(run_after) - unixepoch('now') FROM jobs WHERE kind = 'index'`).
			Scan(&j.Status, &j.Error, &j.Attempts, &j.Retries, &j.RunsAgainSeconds)
		if err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		if err == nil && done(j) {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %+v", what, j)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
