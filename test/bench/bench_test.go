// Package bench measures deepcode on the benchmark repositories (cases_test.go)
// end to end, and writes data/bench/latest.json and latest.md for comparing it
// with DeepWiki and Code Wiki. It runs only when asked, against the real model:
//
//	DEEPCODE_BENCH=1 DEEPCODE_CHAT_MODEL=... go test -run TestBench -timeout 0 -v ./test/bench/
//
// with the DEEPCODE_CHAT_* settings deepcode serve reads (the key only ever in
// the environment). DEEPCODE_BENCH_REPOS=chi,axum picks repositories. Without
// a chat model only indexing, the graph and the retrieval eval are measured.
package bench

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"deepcode/internal/knowledge/chat"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/knowledge/wiki"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	"deepcode/internal/repo/index/graph"
	"deepcode/internal/repo/index/indexer"
	"deepcode/test/eval"
)

// meter sums the model's answers between two reads.
type meter struct {
	mu    sync.Mutex
	calls int
	u     ai.Usage
}

func (m *meter) add(u ai.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.u.InChars, m.u.OutChars, m.u.InTokens, m.u.OutTokens = m.u.InChars+u.InChars, m.u.OutChars+u.OutChars, m.u.InTokens+u.InTokens, m.u.OutTokens+u.OutTokens
}

// take returns what was summed since the last take, and starts again.
func (m *meter) take() (int, ai.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	calls, u := m.calls, m.u
	m.calls, m.u = 0, ai.Usage{}
	return calls, u
}

// rig is deepcode's parts as the server wires them, on a database of the bench's own.
type rig struct {
	db    *sql.DB
	ix    *indexer.Indexer
	cat   *catalog.Catalog
	ret   *retrieval.Retriever
	wiki  *wiki.Generator // nil without a chat model
	chat  *chat.Service
	meter *meter
}

func TestBench(t *testing.T) {
	if os.Getenv("DEEPCODE_BENCH") == "" {
		t.Skip("set DEEPCODE_BENCH=1 to run the benchmark (it calls the model and fetches from GitHub)")
	}
	picked := slices.DeleteFunc(strings.Split(os.Getenv("DEEPCODE_BENCH_REPOS"), ","), func(p string) bool { return p == "" })
	for _, p := range picked {
		if !slices.ContainsFunc(targets, func(tg target) bool { return tg.name == p }) {
			t.Fatalf("DEEPCODE_BENCH_REPOS names %q, which is no benchmark repository", p)
		}
	}
	out, err := filepath.Abs(filepath.Join("..", "..", "data", "bench"))
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(out, "work")
	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	r := setup(t, work)
	rep := report{Started: time.Now().UTC().Format(time.RFC3339), Model: os.Getenv("DEEPCODE_CHAT_MODEL"),
		ReasoningEffort: os.Getenv("DEEPCODE_REASONING_EFFORT"), Deepcode: head()}
	for i, tg := range targets {
		if len(picked) > 0 && !slices.Contains(picked, tg.name) {
			continue
		}
		res := r.measure(t, int64(i+1), tg)
		t.Logf("%s: %d files, %d symbols, %d edges, indexed in %.1fs", tg.name, res.Files, res.Symbols, res.Edges, res.IndexSeconds)
		rep.Repos = append(rep.Repos, res)
		if err := rep.write(out); err != nil { // after each repository, so a stop keeps what is done
			t.Fatal(err)
		}
	}
	t.Logf("wrote %s", filepath.Join(out, "latest.md"))
}

func setup(t *testing.T, work string) *rig {
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(work, "repos"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, filepath.Join(work, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO users(id, username, password_hash) VALUES (1, 'bench', '')`); err != nil {
		t.Fatal(err)
	}
	bus, err := events.New()
	if err != nil {
		t.Fatal(err)
	}
	box, err := gitserver.OpenBox(work, "bench")
	if err != nil {
		t.Fatal(err)
	}
	gitc := &git.Client{Dir: filepath.Join(work, "repos"), AllowProtocol: "https", MaxBytes: 1 << 30}
	servers := &gitserver.Store{DB: db, Box: box}
	r := &rig{db: db, meter: &meter{}, ret: &retrieval.Retriever{DB: db}}
	r.cat = &catalog.Catalog{DB: db, Queue: jobs.New(db, bus, 2*time.Hour), Git: gitc, Servers: servers}
	r.ix = &indexer.Indexer{DB: db, Git: gitc, Servers: servers, Events: bus,
		Limits: indexer.Limits{MaxFiles: 20000, MaxFileBytes: 512 << 10, MaxTotalBytes: 200 << 20}}
	if model := os.Getenv("DEEPCODE_CHAT_MODEL"); model != "" {
		llm := &ai.OpenAI{BaseURL: os.Getenv("DEEPCODE_CHAT_BASE_URL"), APIKey: os.Getenv("DEEPCODE_CHAT_API_KEY"), ModelName: model,
			ReasoningEffort: os.Getenv("DEEPCODE_REASONING_EFFORT"), Timeout: 5 * time.Minute, Measure: r.meter.add}
		r.wiki = &wiki.Generator{DB: db, Catalog: r.cat, Retriever: r.ret, Chat: llm, Language: "English"}
		r.chat = &chat.Service{DB: db, Catalog: r.cat, Retriever: r.ret, Chat: llm}
	}
	return r
}

// measure indexes tg at base and writes its wiki, updates both to head, then
// counts head's graph, runs its eval and asks its questions. A step that
// fails is noted and the rest go on where they can.
func (r *rig) measure(t *testing.T, id int64, tg target) result {
	ctx := context.Background()
	res := result{Repo: tg.name, URL: tg.url, Base: tg.base, Head: tg.head}
	fail := func(step string, err error) {
		res.Errors, res.Failed = append(res.Errors, step+": "+err.Error()), append(res.Failed, step)
		t.Logf("%s %s: %v", tg.name, step, err)
	}
	res.IndexFailed, res.Update.Failed = true, true // till the base and the head snapshot are there
	query := func(step string, dst any, q string, args ...any) bool {
		if err := r.db.QueryRow(q, args...).Scan(dst); err != nil {
			fail(step, err)
			return false
		}
		return true
	}
	const snapshotOf, pages = `SELECT id FROM snapshots WHERE repository_id = ? AND commit_sha = ?`, `SELECT count(*) FROM wiki_pages WHERE snapshot_id = ?`
	owner, name, _ := strings.Cut(strings.TrimPrefix(tg.url, "https://github.com/"), "/")
	if _, err := r.db.Exec(`INSERT INTO repositories(id, url, owner, name, git_server_id) VALUES (?, ?, ?, ?, 1)`, id, tg.url, owner, name); err != nil {
		fail("add", err)
		return res
	}
	start := time.Now()
	if err := r.ix.Run(ctx, id, tg.base, jobs.Progress{}); err != nil {
		fail("index", err)
		return res
	}
	res.IndexSeconds = since(start)
	var base, snap int64
	if !query("base snapshot", &base, snapshotOf, id, tg.base) {
		return res
	}
	res.IndexFailed = false
	if r.wiki != nil {
		res.Wiki = r.writeWiki(ctx, id, tg.base, fail)
		query("base pages", &res.Wiki.Pages, pages, base)
	}

	start = time.Now()
	if err := r.ix.Run(ctx, id, tg.head, jobs.Progress{}); err != nil {
		fail("update index", err)
		return res
	}
	res.Update.IndexSeconds = since(start)
	if !query("head snapshot", &snap, snapshotOf, id, tg.head) {
		return res
	}
	res.Update.Failed = false
	if changed, _, err := r.cat.Changed(ctx, base, snap); err != nil {
		fail("changed files", err)
	} else {
		res.Update.ChangedFiles = len(changed)
	}
	if r.wiki != nil {
		res.Update.Wiki = r.writeWiki(ctx, id, tg.head, fail)
		query("head pages", &res.Update.Wiki.Pages, pages, snap)
		if !res.Wiki.Failed && !res.Update.Wiki.Failed { // else every page would count as rewritten
			// head's pages that are not base's page of the same title with the same text, its links
			// to the commit aside (a carried page's move to head): those the update wrote
			query("pages rewritten", &res.Update.PagesRewritten, `SELECT count(*) FROM wiki_pages h WHERE h.snapshot_id = ?1 AND NOT EXISTS (
				SELECT 1 FROM wiki_pages b WHERE b.snapshot_id = ?2 AND b.title = h.title AND b.content = replace(h.content,
					'/blob/' || (SELECT commit_sha FROM snapshots WHERE id = ?1) || '/', '/blob/' || (SELECT commit_sha FROM snapshots WHERE id = ?2) || '/'))`, snap, base)
		}
	}
	for _, c := range []struct {
		step, q string
		dst     *int
	}{
		{"files", `SELECT count(*) FROM source_files WHERE snapshot_id = ?`, &res.Files},
		{"generated files", `SELECT count(*) FROM source_files WHERE snapshot_id = ? AND generated`, &res.Generated},
		{"languages", `SELECT count(DISTINCT language) FROM source_files WHERE snapshot_id = ?`, &res.Languages},
		{"chunks", `SELECT count(*) FROM chunks WHERE snapshot_id = ?`, &res.Chunks},
		{"symbols", `SELECT count(*) FROM symbols WHERE snapshot_id = ?`, &res.Symbols},
		{"edges", `SELECT count(*) FROM file_edges WHERE snapshot_id = ?`, &res.Edges},
		{"written pages", `SELECT count(*) FROM wiki_pages WHERE snapshot_id = ? AND content <> ''`, &res.Pages},
		{"page citations", `SELECT count(*) FROM wiki_citations c JOIN wiki_pages p ON p.id = c.page_id WHERE p.snapshot_id = ?`, &res.PageCitations},
	} {
		query(c.step, c.dst, c.q, snap)
	}
	if d, err := graph.Diagram(ctx, r.db, snap); err != nil {
		fail("diagram", err)
	} else {
		res.DiagramEdges = strings.Count(d, "-->")
	}
	i := slices.IndexFunc(eval.Corpora, func(c eval.Corpus) bool { return c.Name == tg.name && c.SHA == tg.head })
	if i < 0 {
		fail("eval", errors.New("no corpus pins this repository at head"))
	} else if e, err := eval.Run(ctx, r.ret, snap, eval.Corpora[i]); err != nil {
		fail("eval", err)
	} else {
		res.Eval = &evalResult{Questions: len(eval.Corpora[i].Questions), Recall: e.Recall, MRR: e.MRR, Misses: e.Misses}
	}
	if r.chat != nil {
		for n, q := range tg.questions {
			a := r.ask(ctx, id, q)
			if a.Error != "" {
				fail(fmt.Sprintf("question %d", n+1), errors.New(a.Error))
			}
			res.Answers = append(res.Answers, a)
		}
	}
	return res
}

// writeWiki writes the wiki of the snapshot at sha and measures it.
func (r *rig) writeWiki(ctx context.Context, id int64, sha string, fail func(string, error)) *wikiRun {
	r.meter.take()
	start := time.Now()
	err := r.wiki.Run(ctx, id, sha, jobs.Progress{})
	w := &wikiRun{Seconds: since(start), Failed: err != nil}
	if err != nil {
		fail("wiki "+sha[:7], err)
	}
	calls, u := r.meter.take()
	w.Calls, w.InTokens, w.OutTokens, w.InChars, w.OutChars = calls, u.InTokens, u.OutTokens, u.InChars, u.OutChars
	return w
}

// ask asks q as a user would and times its first words and its end.
func (r *rig) ask(ctx context.Context, id int64, q string) answer {
	a := answer{Question: q}
	start, first := time.Now(), time.Duration(0)
	err := r.chat.Ask(ctx, chat.Request{RepoID: id, Question: q, UserID: 1}, func(event string, data any) {
		switch event {
		case "delta":
			if first == 0 {
				first = time.Since(start)
			}
		case "done":
			if m, ok := data.(chat.Message); ok {
				a.Answer, a.Citations = m.Content, len(m.Citations)
			}
		}
	})
	a.FirstTokenSeconds, a.Seconds = first.Seconds(), since(start)
	if err != nil {
		a.Error = err.Error()
	}
	return a
}

func since(t time.Time) float64 {
	return float64(time.Since(t).Round(100*time.Millisecond)) / float64(time.Second)
}

// head is the deepcode commit measured, marked when the tree has changes.
func head() string {
	out, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		return "unknown"
	}
	if dirty, _ := gitOutput("status", "--porcelain"); dirty != "" {
		out += " with uncommitted changes"
	}
	return out
}

func gitOutput(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}
