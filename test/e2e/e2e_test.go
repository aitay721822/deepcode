// Package e2e drives the whole application (Gin API, sessions and roles,
// Watermill pipeline, SQLite) against local git remotes, a fake GitHub and
// GitLab API, and a fake OpenAI-compatible server.
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"deepcode/internal/app"
	"deepcode/internal/knowledge/chat"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/knowledge/wiki/view"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
)

const goSrc = `package main

import "net/http"

// Server answers HTTP requests.
type Server struct {
	name string
}

// Handle writes a greeting.
func (s *Server) Handle(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("hello " + s.name))
}

func main() {
	http.ListenAndServe(":8080", http.HandlerFunc((&Server{name: "demo"}).Handle))
}
`

const tsSrc = `export class App {
  start() {
    return helper();
  }
}

export const helper = () => 1;
`

type repoView struct {
	ID       string `json:"id"` // a CUID2
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Stars    int    `json:"stars"`
	Snapshot *struct {
		ID         int64  `json:"id"`
		CommitSHA  string `json:"commitSha"`
		WikiStatus string `json:"wikiStatus"`
		ChunkCount int    `json:"chunkCount"`
	} `json:"snapshot"`
	Job         *jobs.Job `json:"job"`
	Permissions []string  `json:"permissions"`
}

// TestEndToEnd: sign in, import, index, wiki (triggered by
// snapshot.published), search, chat with citation checks, the admin console
// with live job output, role-based access, incremental sync and pruning (and
// a pruned commit coming back), and a project on a self-hosted GitLab.
func TestEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	remote := t.TempDir()
	git(t, remote, "init", "-q", "-b", "main")
	git(t, remote, "config", "uploadpack.allowAnySHA1InWant", "true")
	sha1 := commit(t, remote, map[string]string{"main.go": goSrc, "web/app.ts": tsSrc, "README.md": "# Demo\n\nA demo HTTP server.\n"})
	// Both git servers' clone URLs lead to the local remote.
	t.Setenv("GIT_CONFIG_COUNT", "4")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+fileURL(remote)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/acme/demo.git")
	t.Setenv("GIT_CONFIG_KEY_1", "url."+fileURL(remote)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_1", "https://gitlab.example.test/group/sub/app.git")
	t.Setenv("GIT_CONFIG_KEY_2", "gc.auto")
	t.Setenv("GIT_CONFIG_VALUE_2", "0")
	t.Setenv("GIT_CONFIG_KEY_3", "url."+fileURL(remote)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_3", "https://github.com/acme/tools.git")

	api := &fakeAPI{}
	apiSrv := httptest.NewServer(api)
	defer apiSrv.Close()
	llm := &fakeLLM{}
	llmSrv := httptest.NewServer(llm)
	defer llmSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	data := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(data, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	spa := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "spa") })
	a, err := app.New(ctx, app.Config{
		DataDir: data,
		ChatURL: llmSrv.URL + "/v1", ChatModel: "fake-chat",
		EmbedURL: llmSrv.URL + "/v1", EmbedModel: "fake-embed",
		LLMTimeout: time.Minute, Workers: 2, JobTimeout: time.Minute, CacheMB: 1,
		MaxRepoMB: 1, MaxFiles: 100, MaxFileKB: 1024, MaxIndexMB: 10,
		WikiLanguage: "English", GitProtocols: "file",
		AdminPassword: "admin-password", SessionTTL: time.Hour,
	}, db, spa, true)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(stopped)
	}()
	defer func() { cancel(); <-stopped }()
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()

	checkGuardsAndRouting(t, srv.URL)
	admin := newClient(t, srv.URL)
	if code := admin.login("admin", "wrong-password"); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", code)
	}
	if code := admin.login("admin", "admin-password"); code != http.StatusOK {
		t.Fatalf("sign in: %d", code)
	}
	if code := admin.patch("/api/admin/servers/1", fmt.Sprintf(`{"apiUrl":%q}`, apiSrv.URL), nil); code != http.StatusOK {
		t.Fatalf("point GitHub at the fake API: %d", code)
	}

	// Import.
	var repo repoView
	if code := admin.post("/api/repos", `{"url":"https://github.com/acme/demo"}`, &repo); code != http.StatusAccepted {
		t.Fatalf("import: got %d", code)
	}
	if repo.Owner != "acme" || repo.Provider != "github" || repo.Stars != 42 || repo.Job == nil || repo.Job.Kind != jobs.KindIndex || repo.Job.CommitSHA != sha1 {
		t.Fatalf("import response: %+v", repo)
	}
	repoPath := "/api/repos/" + repo.ID
	checkPublicID(t, admin, repo.ID)
	waitFor(t, "index and wiki of the first commit", func() bool {
		admin.get(repoPath, &repo)
		failIfJobFailed(t, repo.Job)
		return repo.Snapshot != nil && repo.Snapshot.WikiStatus == "ready" && repo.Job.Finished()
	})
	if repo.Snapshot.CommitSHA != sha1 {
		t.Fatalf("snapshot pinned to %s, want %s", repo.Snapshot.CommitSHA, sha1)
	}
	firstChunks := repo.Snapshot.ChunkCount
	if code := admin.post("/api/repos", `{"url":"acme/huge"}`, nil); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("import over the size limit: %d", code)
	}

	// Symbols, files and hybrid search.
	var syms []retrieval.SymbolHit
	admin.get(repoPath+"/symbols?q=handle", &syms)
	if len(syms) == 0 || syms[0].QualifiedName != "Server.Handle" || syms[0].Path != "main.go" || syms[0].StartLine != 10 || syms[0].EndLine != 13 {
		t.Fatalf("symbols: %+v", syms)
	}
	var files []struct{ Path string }
	admin.get(repoPath+"/files", &files)
	if len(files) != 3 {
		t.Fatalf("files: %+v", files)
	}
	var file struct{ Content, Language string }
	admin.get(repoPath+"/files/web/app.ts", &file)
	if file.Content != tsSrc || file.Language != "typescript" {
		t.Fatalf("file: %+v", file)
	}
	var found retrieval.Result
	admin.get(repoPath+"/search?q=helper", &found)
	if len(found.Hits) == 0 || found.Hits[0].Path != "web/app.ts" || found.FTS == 0 || found.Vector == 0 || found.Symbol == 0 {
		t.Fatalf("search: %+v", found)
	}

	// Wiki: planned pages, written with commit-pinned citations.
	var wk view.Wiki
	admin.get(repoPath+"/wiki", &wk)
	link := "https://github.com/acme/demo/blob/" + sha1 + "/"
	if wk.Status != "ready" || len(wk.Pages) != 4 || wk.Pages[0].Slug != "1-overview" ||
		!strings.Contains(wk.Pages[0].Content, link) || len(wk.Pages[0].Citations) == 0 {
		t.Fatalf("wiki: %+v", wk)
	}
	checkJobConsole(t, admin, repo.ID)
	checkWikiExport(t, admin, repo.ID, sha1)
	checkBranchesAndHooks(t, admin, repo.ID, sha1)

	// Chat: streamed answer; the grouped citation keeps the real reference
	// and drops the made-up one.
	_, events := admin.events(http.MethodPost, "/api/chat/stream", fmt.Sprintf(`{"repoId":%q,"question":"How does Server.Handle work?"}`, repo.ID))
	if len(events["error"]) > 0 {
		t.Fatalf("chat error: %s", events["error"][0])
	}
	if len(events["conversation"]) != 1 || len(events["evidence"]) != 1 || len(events["delta"]) == 0 || len(events["done"]) != 1 {
		t.Fatalf("chat events: %v", events)
	}
	var answer chat.Message
	json.Unmarshal(events["done"][0], &answer)
	if !strings.Contains(answer.Content, "]("+link+"main.go#L") || strings.Contains(answer.Content, "bogus.go") ||
		len(answer.Citations) != 1 || answer.Citations[0].Path != "main.go" {
		t.Fatalf("answer: %+v", answer)
	}
	checkUsage(t, admin, repo.ID)
	var conv struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(events["conversation"][0], &conv)
	var stored chat.Conversation
	admin.get(fmt.Sprintf("/api/conversations/%d", conv.ID), &stored)
	if len(stored.Messages) != 2 || stored.CommitSHA != sha1 {
		t.Fatalf("conversation: %+v", stored)
	}
	checkAccess(t, srv.URL, admin, repo.ID, conv.ID)
	checkSignUp(t, srv.URL, admin)

	// A question none of whose words match the code, as one in Chinese, is
	// searched again with the English terms the model gives for it, put first
	// since the text search keeps only a question's first 16 words. A question
	// whose words match costs no such request.
	if n := llm.termRequests(); n != 0 {
		t.Fatalf("search terms asked for %d questions whose words match", n)
	}
	for i, q := range []string{"伺服器怎麼處理請求？",
		"quokka narwhal zeppelin xylophone kumquat platypus walrus tundra velvet orchid pebble quilt saffron meadow lantern jigsaw?"} {
		_, events = admin.events(http.MethodPost, "/api/chat/stream", fmt.Sprintf(`{"repoId":%q,"question":"%s"}`, repo.ID, q))
		var searched struct {
			FTS int `json:"fts"`
		}
		if len(events["evidence"]) == 1 {
			json.Unmarshal(events["evidence"][0], &searched)
		}
		if searched.FTS == 0 || len(events["done"]) != 1 || llm.termRequests() != i+1 {
			t.Fatalf("%s: %d search-term requests, %v", q, llm.termRequests(), events)
		}
	}

	// Sync with nothing new is a no-op.
	var synced struct {
		UpToDate bool      `json:"upToDate"`
		Job      *jobs.Job `json:"job"`
	}
	if code := admin.post(repoPath+"/sync", `{}`, &synced); code != http.StatusOK || !synced.UpToDate {
		t.Fatalf("sync without changes: %d %+v", code, synced)
	}

	// Incremental sync, followed live: only chunks of changed files are embedded again.
	before := llm.chunkInputs()
	sha2 := commit(t, remote, map[string]string{
		"web/app.ts":  "import { add } from \"./util\";\n\n" + strings.Replace(tsSrc, "  start() {", "  stop() {}\n\n  start() {", 1),
		"web/util.ts": "export function add(a: number, b: number) {\n  return a + b;\n}\n",
	})
	if code := admin.post(repoPath+"/sync", `{}`, &synced); code != http.StatusAccepted || synced.Job == nil {
		t.Fatalf("sync: %d %+v", code, synced)
	}
	checkLiveOutput(t, admin, synced.Job.ID)
	waitFor(t, "index and wiki of the second commit", func() bool {
		admin.get(repoPath, &repo)
		failIfJobFailed(t, repo.Job)
		return repo.Snapshot.CommitSHA == sha2 && repo.Snapshot.WikiStatus == "ready" && repo.Job.Finished()
	})
	var changed int
	db.QueryRow(`SELECT count(*) FROM chunks WHERE snapshot_id = ? AND path IN ('web/app.ts', 'web/util.ts')`, repo.Snapshot.ID).Scan(&changed)
	if got := llm.chunkInputs() - before; got != changed || changed == 0 || changed >= repo.Snapshot.ChunkCount {
		t.Fatalf("sync embedded %d chunks, want only the %d changed of %d", got, changed, repo.Snapshot.ChunkCount)
	}
	var mainSymbols int
	db.QueryRow(`SELECT count(*) FROM symbols WHERE snapshot_id = ? AND path = 'main.go'`, repo.Snapshot.ID).Scan(&mainSymbols)
	if mainSymbols != 3 {
		t.Fatalf("unchanged main.go carried %d symbols into the new snapshot, want 3", mainSymbols)
	}
	var linked int
	db.QueryRow(`SELECT count(*) FROM file_edges WHERE snapshot_id = ? AND src = 'web/app.ts' AND dst = 'web/util.ts'`, repo.Snapshot.ID).Scan(&linked)
	if linked != 1 {
		t.Fatal("the new snapshot's import of web/util.ts was not linked")
	}

	// A follow-up stays on its conversation's commit while that is indexed.
	_, events = admin.events(http.MethodPost, "/api/chat/stream", fmt.Sprintf(`{"repoId":%q,"conversationId":%d,"question":"And the server?"}`, repo.ID, conv.ID))
	if len(events["done"]) != 1 {
		t.Fatalf("follow-up on the older commit: %v", events)
	}
	json.Unmarshal(events["conversation"][0], &stored)
	if stored.CommitSHA != sha1 {
		t.Fatalf("follow-up answered at %s, want its own %s", stored.CommitSHA, sha1)
	}

	// A third commit prunes the first snapshot's data but keeps its row.
	sha3 := commit(t, remote, map[string]string{"README.md": "# Demo\n\nA demo HTTP server, now documented.\n"})
	admin.post(repoPath+"/sync", `{}`, &synced)
	waitFor(t, "index and wiki of the third commit", func() bool {
		admin.get(repoPath, &repo)
		failIfJobFailed(t, repo.Job)
		return repo.Snapshot.CommitSHA == sha3 && repo.Snapshot.WikiStatus == "ready" && repo.Job.Finished()
	})
	if n := llm.planRequests(); n != 1 {
		t.Fatalf("the wiki was planned %d times, want once: the later commits change few files, so they keep its pages", n)
	}
	var status string
	var leftovers int
	db.QueryRow(`SELECT status FROM snapshots WHERE commit_sha = ? AND repository_id = (SELECT id FROM repositories WHERE public_id = ?)`, sha1, repo.ID).Scan(&status)
	db.QueryRow(`SELECT count(*) FROM chunks c JOIN snapshots s ON s.id = c.snapshot_id WHERE s.commit_sha = ? AND s.repository_id = (SELECT id FROM repositories WHERE public_id = ?)`, sha1, repo.ID).Scan(&leftovers)
	if status != "pruned" || leftovers != 0 {
		t.Fatalf("first snapshot: status %q with %d chunks, want pruned with none", status, leftovers)
	}

	// Reopened, the conversation on the pruned commit shows the published one,
	// and its next question moves it there.
	admin.get(fmt.Sprintf("/api/conversations/%d", conv.ID), &stored)
	if stored.CommitSHA != sha3 || len(stored.Messages) != 4 {
		t.Fatalf("reopened conversation at %s with %d messages, want %s", stored.CommitSHA, len(stored.Messages), sha3)
	}
	var cited struct{ Content string }
	if code := admin.get(fmt.Sprintf("%s/files/main.go?snapshot=%d", repoPath, stored.SnapshotID), &cited); code != http.StatusOK || cited.Content == "" {
		t.Fatalf("cited file at the reopened conversation's snapshot %d: %d", stored.SnapshotID, code)
	}
	_, events = admin.events(http.MethodPost, "/api/chat/stream", fmt.Sprintf(`{"repoId":%q,"conversationId":%d,"question":"And main?"}`, repo.ID, conv.ID))
	if len(events["done"]) != 1 {
		t.Fatalf("follow-up: %v", events)
	}
	json.Unmarshal(events["conversation"][0], &stored)
	if stored.CommitSHA != sha3 {
		t.Fatalf("follow-up answered at %s, want %s", stored.CommitSHA, sha3)
	}

	// A failed wiki is written again by Sync, without indexing again.
	db.Exec(`UPDATE snapshots SET wiki_status = 'failed' WHERE id = ?`, repo.Snapshot.ID)
	var rewrite struct {
		Job *jobs.Job `json:"job"`
	}
	if code := admin.post(repoPath+"/sync", `{}`, &rewrite); code != http.StatusAccepted || rewrite.Job == nil || rewrite.Job.Kind != jobs.KindWiki {
		t.Fatalf("sync after a failed wiki: %d %+v", code, rewrite.Job)
	}
	waitFor(t, "the wiki written again", func() bool {
		admin.get(repoPath, &repo)
		failIfJobFailed(t, repo.Job)
		return repo.Snapshot.WikiStatus == "ready" && repo.Job.Finished()
	})

	checkGitLab(t, admin, api, apiSrv.URL, sha3)
	checkOAuth(t, admin)
	ivy, tools := checkImportPolicy(t, srv.URL, admin, repo.ID)
	checkJobErrors(t, db, admin, ivy, tools)
	checkJobEvents(t, srv.URL, admin, repo.ID, tools, func() {
		checkPrunedCommitReturns(t, db, admin, remote, repoPath, sha1, firstChunks)
		if code := admin.post(fmt.Sprintf("/api/repos/%s/sync", tools), `{}`, nil); code != http.StatusAccepted {
			t.Fatalf("sync acme/tools: %d", code)
		}
	})
	checkDownloadLimit(t, admin)
}

// checkGuardsAndRouting covers the middleware and the Gin fallbacks.
func checkGuardsAndRouting(t *testing.T, base string) {
	t.Helper()
	anon := newClient(t, base)
	req, _ := http.NewRequest(http.MethodGet, base+"/api/health", nil)
	req.Host = "evil.example"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Host: %v %v", resp, err)
	}
	if resp, _ := http.Post(base+"/api/repos", "text/plain", strings.NewReader("{}")); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: got %d", resp.StatusCode)
	}
	var notFound map[string]string
	if code := anon.get("/api/nope", &notFound); code != http.StatusNotFound || notFound["error"] == "" {
		t.Fatalf("unknown API route: %d %v", code, notFound)
	}
	if code := anon.get("/api/health", nil); code != http.StatusOK {
		t.Fatalf("health needs no session: %d", code)
	}
	if code := anon.get("/api/repos", nil); code != http.StatusUnauthorized {
		t.Fatalf("repositories without a session: %d", code)
	}
	resp, err := http.Get(base + "/repos/1?page=1-overview")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "spa" {
		t.Fatalf("SPA route: %d %q", resp.StatusCode, body)
	}
}

// checkPrunedCommitReturns: the branch goes back to a commit whose snapshot
// was pruned, after a prune that stopped midway left a file row behind; the
// commit is indexed again whole.
func checkPrunedCommitReturns(t *testing.T, db *sql.DB, admin *client, remote, repoPath, sha string, chunks int) {
	t.Helper()
	var repo repoView
	admin.get(repoPath, &repo)
	res, err := db.Exec(`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count)
		SELECT s.id, f.path, f.language, f.content_hash, f.size, f.line_count FROM source_files f, snapshots s
		WHERE f.snapshot_id = ? AND f.path = 'main.go' AND s.repository_id = (SELECT id FROM repositories WHERE public_id = ?) AND s.commit_sha = ? AND s.status = 'pruned'`,
		repo.Snapshot.ID, repo.ID, sha)
	if n, _ := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("leave a file row in the pruned snapshot: %d rows, %v", n, err)
	}
	git(t, remote, "update-ref", "refs/heads/main", sha)
	if code := admin.post(repoPath+"/sync", `{}`, nil); code != http.StatusAccepted {
		t.Fatalf("sync back to %.7s: %d", sha, code)
	}
	waitFor(t, "the pruned commit indexed again", func() bool {
		admin.get(repoPath, &repo)
		failIfJobFailed(t, repo.Job)
		return repo.Snapshot.CommitSHA == sha && repo.Job.Finished()
	})
	if repo.Snapshot.ChunkCount != chunks {
		t.Fatalf("the commit indexed again has %d chunks, want %d", repo.Snapshot.ChunkCount, chunks)
	}
}
