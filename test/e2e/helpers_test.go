package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepcode/internal/platform/jobs"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "core.autocrlf=false", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes files into the repository at dir, commits and returns the SHA.
func commit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "change")
	return git(t, dir, "rev-parse", "HEAD")
}

func fileURL(dir string) string {
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

func failIfJobFailed(t *testing.T, j *jobs.Job) {
	t.Helper()
	if j != nil && (j.Status == "failed" || j.Error != "") {
		t.Fatalf("job failed: %+v", *j)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// client is one browser: its cookie jar holds the session.
type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t, base, &http.Client{Jar: jar}}
}

// do sends method path with a JSON body ("" for none) and returns the response.
func (c *client) do(method, path, body string) *http.Response {
	c.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

// call sends a request, decodes a JSON answer into v unless v is nil, and
// returns the status.
func (c *client) call(method, path, body string, v any) int {
	c.t.Helper()
	resp := c.do(method, path, body)
	defer resp.Body.Close()
	if v != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			c.t.Fatalf("%s %s: %d: %v", method, path, resp.StatusCode, err)
		}
	}
	return resp.StatusCode
}

func (c *client) get(path string, v any) int         { return c.call(http.MethodGet, path, "", v) }
func (c *client) post(path, body string, v any) int  { return c.call(http.MethodPost, path, body, v) }
func (c *client) patch(path, body string, v any) int { return c.call(http.MethodPatch, path, body, v) }
func (c *client) delete(path string) int             { return c.call(http.MethodDelete, path, "", nil) }
func (c *client) login(username, password string) int {
	return c.post("/api/auth/login", creds(username, password), nil)
}

func creds(username, password string) string {
	return fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
}

// events sends a request and collects the server-sent events by name until
// the server ends the stream.
func (c *client) events(method, path, body string) (int, map[string][]json.RawMessage) {
	c.t.Helper()
	resp := c.do(method, path, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	events := map[string][]json.RawMessage{}
	for _, block := range strings.Split(string(raw), "\n\n") {
		var name, data string
		for _, line := range strings.Split(block, "\n") {
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				name = v
			} else if v, ok := strings.CutPrefix(line, "data: "); ok {
				data = v
			}
		}
		if name != "" {
			events[name] = append(events[name], json.RawMessage(data))
		}
	}
	return resp.StatusCode, events
}
