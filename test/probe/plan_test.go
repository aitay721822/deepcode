package probe

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"deepcode/cmd/deepcode/config"
	"deepcode/internal/app"
	"deepcode/internal/knowledge/retrieval"
	retrievalstore "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/knowledge/wiki"
	"deepcode/internal/knowledge/wiki/outline/place"
	wikistore "deepcode/internal/knowledge/wiki/store"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/events"
	"deepcode/internal/platform/jobs"
	jobstore "deepcode/internal/platform/jobs/store"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
	catalogstore "deepcode/internal/repo/catalog/store"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/gitserver"
	serverstore "deepcode/internal/repo/gitserver/store"
	"deepcode/internal/repo/index/graph"
	graphstore "deepcode/internal/repo/index/graph/store"
	"deepcode/internal/repo/index/indexer"
	indexstore "deepcode/internal/repo/index/indexer/store"
	"deepcode/internal/repo/index/vectorstore"
	"deepcode/test/bench/shape"
)

// planRepos are the bench's repositories (test/bench/cases_test.go): owner
// and name, the base commit and DeepWiki's page count.
var planRepos = map[string]struct {
	owner, base string
	deepwiki    int
}{
	"chi":       {"go-chi", "04f7f55a7b5ddb2ecd79a8d813b2256c1113b8a9", 28},
	"zustand":   {"pmndrs", "b4883239492c687244d92cf50bf4fae7125ae5ad", 42},
	"express":   {"expressjs", "6389ae5e397acf28dd66765fbcd9c487f7a61e17", 33},
	"eshop":     {"dotnet", "dc7ea499cd356924fb6689b3702964a5869dbae9", 37},
	"petclinic": {"spring-projects", "0d064b04958da7715a9138459bafbde3bd10949d", 35},
	"gson":      {"google", "57731049da3e415be3d5d34db1ca3b80f3c73810", 23},
	"axum":      {"tokio-rs", "40d061fdacd468217c7a609bede16357276b6c40", 61},
}

// planRun is one plan's measure, logged as a "PROBE-C" line of JSON.
type planRun struct {
	Repo     string        `json:"repo"`
	Run      int           `json:"run"`
	Planning wiki.Planning `json:"planning"`
	Prompt   int           `json:"promptTokens"`
	Output   int           `json:"outputTokens"`
	Shape    shape.Shape   `json:"shape"`
	Unnamed  int           `json:"unnamed"`
	DeepWiki int           `json:"deepwiki"`
	Units    []planUnit    `json:"units"` // the code units of cap 2 or more
	Error    string        `json:"error,omitempty"`
	Pages    []string      `json:"pages"`
}

// planUnit is a code unit's cap and the pages planned for it.
type planUnit struct {
	Key   string `json:"key"`
	Cap   int    `json:"cap"`
	Pages int    `json:"pages"`
}

// TestPlan is Probe C: the planner alone on a bench repository's base
// snapshot, no page written. It logs the plan's time, requests, prompt
// tokens, contract counts, shape and, per code unit of cap 2 or more, the
// pages planned against its cap. DEEPCODE_PROBE=C, DEEPCODE_PROBE_REPO (a
// bench repository), DEEPCODE_PROBE_RUN (the run's number, from 1),
// DEEPCODE_PROBE_DIR (the indexed snapshot, kept between runs),
// DEEPCODE_EVAL_GIT holding the repository's bare clone, DEEPCODE_CHAT_* as
// serve reads them; the model is asked by Completions.
func TestPlan(t *testing.T) {
	if os.Getenv("DEEPCODE_PROBE") != "C" {
		t.Skip("set DEEPCODE_PROBE=C to probe the planner on a bench repository")
	}
	name, dir := os.Getenv("DEEPCODE_PROBE_REPO"), os.Getenv("DEEPCODE_PROBE_DIR")
	repo, ok := planRepos[name]
	run, err := strconv.Atoi(os.Getenv("DEEPCODE_PROBE_RUN"))
	if !ok || err != nil || run < 1 || dir == "" || os.Getenv("DEEPCODE_EVAL_GIT") == "" {
		t.Fatal("set DEEPCODE_PROBE_REPO to a bench repository, DEEPCODE_PROBE_RUN to 1 or more, DEEPCODE_PROBE_DIR and DEEPCODE_EVAL_GIT")
	}
	cfg, _, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ChatAPI = ai.Completions
	model, _ := app.Providers(cfg)
	if model == nil {
		t.Fatal("the probe needs DEEPCODE_CHAT_MODEL")
	}
	ctx := context.Background()
	db, snap := indexed(t, filepath.Join(dir, name), filepath.Join(os.Getenv("DEEPCODE_EVAL_GIT"), name+".git"), repo.owner, name, repo.base)
	for _, reset := range []string{`DELETE FROM wiki_pages WHERE snapshot_id = ?`, `UPDATE snapshots SET wiki_status = 'none', wiki_labels = NULL WHERE id = ?`} {
		if _, err := db.Exec(reset, snap); err != nil {
			t.Fatal(err)
		}
	}
	res := planRun{Repo: name, Run: run, DeepWiki: repo.deepwiki}
	var mu sync.Mutex
	model.BaseURL, model.Measure = refuseWrites(t, model.BaseURL), func(u ai.Usage) {
		mu.Lock()
		defer mu.Unlock()
		res.Prompt, res.Output = res.Prompt+u.InTokens, res.Output+u.OutTokens
	}
	graphs := &graph.Graph{Store: &graphstore.Store{DB: db}}
	store := &wikistore.Store{DB: db}
	g := &wiki.Generator{Store: store, Graph: graphs, Chat: model, Language: "English", Parallel: 1,
		Catalog:   &catalog.Catalog{Store: &catalogstore.Store{DB: db}, Queue: &jobs.Queue{Store: &jobstore.Store{DB: db}}},
		Retriever: &retrieval.Retriever{Store: &retrievalstore.Store{DB: db}, Graph: graphs},
		Planned:   func(p wiki.Planning) { res.Planning = p }}
	if err := g.Run(ctx, 1, repo.base, jobs.Progress{}); err != nil && !strings.Contains(err.Error(), refusal) {
		res.Error = err.Error()
	}
	pages, err := store.Pages(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	if res.Shape, err = shape.Measure(ctx, db, snap); err != nil {
		t.Fatal(err)
	}
	res.Unnamed, res.Units = len(res.Shape.Unnamed), atCap(t, ctx, db, snap, pages)
	for _, p := range pages {
		res.Pages = append(res.Pages, strings.Repeat("  ", min(p.Parent+1, 1))+p.Title)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PROBE-C %s", b)
	if len(pages) == 0 {
		t.Errorf("%s run %d planned nothing: %s", name, run, res.Error)
	}
}

// refusal is what refuseWrites answers a page's write with.
const refusal = "the probe writes no page"

// refuseWrites is a server in front of the chat server at target that passes
// on the structured requests, the plan's, and refuses every other, a page's
// write, which comes once the plan is stored.
func refuseWrites(t *testing.T, target string) string {
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	pass := httputil.NewSingleHostReverseProxy(u)
	direct := pass.Director
	pass.Director = func(r *http.Request) { direct(r); r.Host = u.Host }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Contains(body, []byte(`"response_format"`)) {
			http.Error(w, `{"error":{"message":"`+refusal+`"}}`, http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		pass.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// indexed is the base snapshot of the repository in the bare clone src at
// sha, indexed as an index job does with cmd/deepcode's limits and no
// vectors, into a database in work, which a later run reuses.
func indexed(t *testing.T, work, src, owner, name, sha string) (*sql.DB, int64) {
	ctx := context.Background()
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, filepath.Join(work, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var snap sql.NullInt64
	if db.QueryRow(`SELECT ready_snapshot_id FROM repositories WHERE id = 1`).Scan(&snap); snap.Valid {
		return db, snap.Int64
	}
	// The git server fetches url+".git".
	if _, err := db.Exec(`INSERT OR REPLACE INTO repositories(id, url, owner, name) VALUES (1, ?, ?, ?)`, // a run that died indexing left the row
		"file:///"+strings.TrimPrefix(filepath.ToSlash(strings.TrimSuffix(src, ".git")), "/"), owner, name); err != nil {
		t.Fatal(err)
	}
	bus, err := events.New()
	if err != nil {
		t.Fatal(err)
	}
	box, err := gitserver.OpenBox(work, "probe")
	if err != nil {
		t.Fatal(err)
	}
	ix := &indexer.Indexer{Store: &indexstore.Store{DB: db}, Graph: &graph.Graph{Store: &graphstore.Store{DB: db}},
		Git: &git.Client{Dir: work, AllowProtocol: "file"}, Servers: &gitserver.Service{Store: &serverstore.Store{DB: db}, Box: box}, Events: bus,
		Vectors: &vectorstore.SQLite{DB: db}, Limits: indexer.Limits{MaxFiles: 20000, MaxFileBytes: 512 << 10, MaxTotalBytes: 200 << 20}}
	if err := ix.Run(ctx, 1, sha, jobs.Progress{}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT ready_snapshot_id FROM repositories WHERE id = 1`).Scan(&snap); err != nil {
		t.Fatal(err)
	}
	return db, snap.Int64
}

// atCap is, per code unit of cap 2 or more, its cap as the planner weighs it
// (wiki.Generator's structure and place.Ceiling at the default ceiling of 64)
// and the pages planned for it: those not heading a section whose units hold
// its key.
func atCap(t *testing.T, ctx context.Context, db *sql.DB, snap int64, pages []wikistore.Page) []planUnit {
	store := &wikistore.Store{DB: db}
	paths, err := store.Paths(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := store.Lines(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string][]string{}
	if err := store.Declared(ctx, snap, func(p, name string) {
		if !slices.Contains(declared[p], name) {
			declared[p] = append(declared[p], name)
		}
	}); err != nil {
		t.Fatal(err)
	}
	edges, err := (&graphstore.Store{DB: db}).Edges(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	code, asides := place.Units(paths, lines, declared, edges)
	p := 0
	for _, u := range code {
		p += u.Cap
	}
	place.Shares(asides, lines, p)
	code, _, _ = place.Ceiling(code, asides, edges, lines, 64)
	heads := map[int]bool{}
	for _, pg := range pages {
		heads[pg.Parent] = true
	}
	var out []planUnit
	for _, u := range code {
		if u.Cap < 2 {
			continue
		}
		n := 0
		for _, pg := range pages {
			if !heads[pg.Position] && pg.Files != nil && slices.Contains(pg.Files.Units, u.Key) {
				n++
			}
		}
		out = append(out, planUnit{Key: u.Key, Cap: u.Cap, Pages: n})
	}
	return out
}
