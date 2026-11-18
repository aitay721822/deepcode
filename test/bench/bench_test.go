// Package bench measures deepcode on the benchmark repositories (cases_test.go) end to end, and writes
// data/bench/latest.json and latest.md for comparing it with DeepWiki and Code Wiki. It runs only when
// asked, against the real model:
//
//	DEEPCODE_BENCH=1 DEEPCODE_CHAT_MODEL=... go test -run TestBench -timeout 0 -v ./test/bench/
//
// with the DEEPCODE_CHAT_* settings deepcode serve reads (the key only ever in the environment), and
// DEEPCODE_EMBED_* for vector search, read as serve reads them (lexical search only without
// DEEPCODE_EMBED_MODEL). DEEPCODE_BENCH_REPOS=chi,axum picks repositories, and DEEPCODE_BENCH_PARALLEL=1,2
// writes each first wiki once per number of pages written at a time (DEEPCODE_WIKI_PARALLEL, else 1, by
// default), the last one kept. Without a chat model only indexing, the graph and the retrieval eval are
// measured; DEEPCODE_BENCH_WIKI=0 leaves the wikis out, for a run of the answers alone, and
// DEEPCODE_BENCH_ANSWERS=0 the answers, for one of the wikis alone. An agent
// (DEEPCODE_CHAT_PROVIDER=acp) is asked DEEPCODE_ACP_SESSIONS questions at a time, a server one. The
// answers are judged by DEEPCODE_JUDGE_MODEL at DEEPCODE_JUDGE_BASE_URL with DEEPCODE_JUDGE_API_KEY, at
// that server's own reasoning effort and temperature, else by the chat model (score.Model). TestJudge
// re-judges a stored run's answers (judge_test.go).
package bench

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"deepcode/internal/knowledge/chat"
	chatstore "deepcode/internal/knowledge/chat/store"
	"deepcode/internal/knowledge/retrieval"
	retrievalstore "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/knowledge/wiki"
	wikistore "deepcode/internal/knowledge/wiki/store"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/acp"
	"deepcode/internal/platform/ai/tokens"
	"deepcode/internal/platform/cache"
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
	graphstore "deepcode/internal/repo/index/graphstore"
	"deepcode/internal/repo/index/indexer"
	indexstore "deepcode/internal/repo/index/indexer/store"
	"deepcode/internal/repo/index/indexer/vectorstore"
	"deepcode/test/bench/score"
	"deepcode/test/bench/shape"
	"deepcode/test/bench/trace"
	"deepcode/test/eval"
)

// meter sums the model's answers between two reads, and keeps each: the chat
// model's Meter. The answers to a question, which questions asked at a time
// would mix, it keeps apart by the question (asking) for requests.
type meter struct {
	mu    sync.Mutex
	calls int
	u     ai.Usage
	each  []ai.Usage
	asked map[*answer][]ai.Usage
}

// asking keys a question's context: its value is the question's *answer.
type asking struct{}

func (m *meter) Record(ctx context.Context, _ string, u ai.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := ctx.Value(asking{}).(*answer); ok {
		m.asked[a] = append(m.asked[a], u)
		return
	}
	m.calls++
	m.u.Add(u)
	m.each = append(m.each, u)
}

// requests returns the answers to a's question in the order they came, and
// forgets them.
func (m *meter) requests(a *answer) []ai.Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	each := m.asked[a]
	delete(m.asked, a)
	return each
}

// TestMeter: a take returns the model's answers since the last, summed and
// each in order, and starts again; a question's are its own.
func TestMeter(t *testing.T) {
	m, a := meter{asked: map[*answer][]ai.Usage{}}, &answer{}
	m.Record(context.Background(), "m", ai.Usage{InTokens: 5, OutTokens: 1})
	m.take()
	m.Record(context.Background(), "m", ai.Usage{InTokens: 10, OutTokens: 2})
	m.Record(context.WithValue(context.Background(), asking{}, a), "m", ai.Usage{InTokens: 7})
	m.Record(context.Background(), "m", ai.Usage{InTokens: 20, OutTokens: 3, CachedTokens: 4})
	if calls, u, each := m.take(); calls != 2 || u != (ai.Usage{InTokens: 30, OutTokens: 5, CachedTokens: 4}) ||
		!slices.Equal(each, []ai.Usage{{InTokens: 10, OutTokens: 2}, {InTokens: 20, OutTokens: 3, CachedTokens: 4}}) {
		t.Errorf("%d calls, %+v, each %+v", calls, u, each)
	}
	if each, again := m.requests(a), m.requests(a); !slices.Equal(each, []ai.Usage{{InTokens: 7}}) || again != nil {
		t.Errorf("the question's: %+v, then %+v", each, again)
	}
}

// take returns what was summed since the last take, and each answer summed in
// the order it came, and starts again.
func (m *meter) take() (int, ai.Usage, []ai.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	calls, u, each := m.calls, m.u, m.each
	m.calls, m.u, m.each = 0, ai.Usage{}, nil
	return calls, u, each
}

// rig is deepcode's parts as the server wires them, on a database of the bench's own.
type rig struct {
	db       *sql.DB
	ix       *indexer.Indexer
	cat      *catalog.Catalog
	ret      *retrieval.Retriever
	wiki     *wiki.Generator // nil without a chat model
	chat     *chat.Service
	judge    ai.Chat // the answers' (score.Model)
	meter    *meter
	asks     int      // questions asked at a time: an agent's sessions (design D10), else 1
	parallel []int    // the first wiki's runs, by pages written at a time
	last     *wikiRun // the wiki being written, which the generator's hooks tell of its plan and carried pages
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
	for _, p := range strings.Split(cmp.Or(os.Getenv("DEEPCODE_BENCH_PARALLEL"), os.Getenv("DEEPCODE_WIKI_PARALLEL"), "1"), ",") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 1 {
			t.Fatalf("DEEPCODE_BENCH_PARALLEL holds %q, which is no number of pages at a time", p)
		}
		r.parallel = append(r.parallel, n)
	}
	rep := report{Started: time.Now().UTC().Format(time.RFC3339), ReasoningEffort: os.Getenv("DEEPCODE_REASONING_EFFORT"),
		Temperature: os.Getenv("DEEPCODE_TEMPERATURE"), Deepcode: head(), Judged: true, JudgeModel: os.Getenv("DEEPCODE_JUDGE_MODEL")}
	if r.chat != nil { // an agent by its name and model: claude-agent-acp/default
		rep.Model, rep.ChatSchema, rep.Asks = r.chat.Chat.Name(), r.chat.Chat.SchemaMode(), r.asks
	}
	if r.ret.Embed != nil {
		rep.EmbedModel = r.ret.Embed.ModelName
	}
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
	servers := &gitserver.Service{Store: &serverstore.Store{DB: db}, Box: box}
	graphs := &graph.Graph{Store: &graphstore.Store{DB: db}}
	embed, err := eval.Embedder() // nil without DEEPCODE_EMBED_MODEL: no vector search
	if err != nil {
		t.Fatal(err)
	}
	vectors := &vectorstore.SQLite{DB: db}
	r := &rig{db: db, meter: &meter{asked: map[*answer][]ai.Usage{}}, asks: 1, ret: &retrieval.Retriever{Store: &retrievalstore.Store{DB: db}, Graph: graphs,
		Embed: embed, Vectors: vectors, Cache: cache.New(1 << 20)}}
	r.cat = &catalog.Catalog{Store: &catalogstore.Store{DB: db}, Queue: jobs.New(&jobstore.Store{DB: db}, bus, 2*time.Hour), Git: gitc, Servers: servers}
	r.ix = &indexer.Indexer{Store: &indexstore.Store{DB: db}, Graph: graphs, Git: gitc, Servers: servers, Events: bus,
		Embed: embed, Vectors: vectors, Limits: indexer.Limits{MaxFiles: 20000, MaxFileBytes: 512 << 10, MaxTotalBytes: 200 << 20}}
	counter, err := tokens.Load(os.Getenv("DEEPCODE_CHAT_TOKENIZER")) // OpenAI's o200k_base when unset, as serve counts
	if err != nil {
		t.Fatalf("DEEPCODE_CHAT_TOKENIZER: %v", err)
	}
	llm, err := eval.Chat(work, r.meter, counter.Count) // a server's, or an agent's on its subscription
	if err != nil {
		t.Fatal(err)
	}
	if llm != nil {
		if c, ok := llm.(interface{ Close() }); ok { // an agent's process ends with the bench
			t.Cleanup(c.Close)
		}
		if a, ok := llm.(*acp.Agent); ok { // its questions asked as many at a time as it takes prompts: its set within 10 minutes
			r.asks = max(a.Sessions, 1)
		}
		for u := 2; u <= r.asks; u++ { // a user of its own for each question asked at a time
			if _, err := db.Exec(`INSERT INTO users(id, username, password_hash) VALUES (?, ?, '')`, u, fmt.Sprint("bench", u)); err != nil {
				t.Fatal(err)
			}
		}
		pages, err := strconv.Atoi(cmp.Or(os.Getenv("DEEPCODE_WIKI_MAX_PAGES"), "0")) // 0, the default, when unset
		if err != nil || pages < 0 {
			t.Fatalf("DEEPCODE_WIKI_MAX_PAGES holds %q, which is no number of pages", os.Getenv("DEEPCODE_WIKI_MAX_PAGES"))
		}
		if os.Getenv("DEEPCODE_BENCH_WIKI") != "0" {
			r.wiki = &wiki.Generator{Store: &wikistore.Store{DB: db}, Graph: graphs, Catalog: r.cat, Retriever: r.ret, Chat: llm, Language: "English", MaxPages: pages,
				Planned: func(p wiki.Planning) { r.last.Planning = &p }, Touched: func(_, why string) { r.last.Touched[why]++ },
				Written: func(p wiki.Writing) { r.last.wrote(p) }}
		}
		r.chat, r.judge = &chat.Service{Store: &chatstore.Store{DB: db}, Catalog: r.cat, Retriever: r.ret, Chat: llm, Tokens: counter}, score.Model(llm)
	}
	return r
}

// measure indexes tg at base and writes its wiki, updates both to head, then
// counts head's graph, runs its eval and asks its questions. A step that
// fails is noted and the rest go on where they can.
func (r *rig) measure(t *testing.T, id int64, tg target) result {
	ctx := context.Background()
	res := result{Repo: tg.name, URL: tg.url, Base: tg.base, Head: tg.head, DeepWiki: tg.pages}
	var mu sync.Mutex // fail's: questions asked at a time fail at once
	fail := func(step string, err error) {
		mu.Lock()
		defer mu.Unlock()
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
	for i, n := range r.parallel {
		if r.wiki == nil {
			break
		}
		if i > 0 { // written again from nothing
			if _, err := r.db.Exec(`DELETE FROM wiki_pages WHERE snapshot_id = ?1; UPDATE snapshots SET wiki_status = 'none' WHERE id = ?1`, base); err != nil {
				fail("wiki again", err)
				break
			}
		}
		r.wiki.Parallel = n
		res.Wiki = r.writeWiki(ctx, id, tg.base, fail)
		res.Wiki.Parallel = n
		query("base pages", &res.Wiki.Pages, pages, base)
		res.WikiRuns = append(res.WikiRuns, *res.Wiki)
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
			if n, err := rewritten(ctx, r.db, base, snap); err != nil {
				fail("pages rewritten", err)
			} else {
				res.Update.PagesRewritten = n
			}
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
	if s, err := span(ctx, r.db, snap); err != nil {
		fail("citation span", err)
	} else {
		res.CitationSpan = s
	}
	if tree, err := pageTree(ctx, r.db, snap); err != nil {
		fail("page tree", err)
	} else {
		res.Tree = tree
	}
	if heads, err := headings(ctx, r.db, snap); err != nil {
		fail("topics", err)
	} else if n, err := toolingPages(ctx, r.db, snap); err != nil {
		fail("topics", err)
	} else if s, err := shape.Measure(ctx, r.db, snap); err != nil {
		fail("topics", err)
	} else if len(res.Tree) > 0 { // a wiki to measure
		c := cover(tg.topics(), heads)
		c.ToolingPages, c.Unnamed = n, s.Unnamed
		res.Topics, res.Plan = &c, &s
	}
	if d, err := r.ix.Graph.Diagram(ctx, snap); err != nil {
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
	if r.chat != nil && os.Getenv("DEEPCODE_BENCH_ANSWERS") != "0" {
		res.Answers = make([]answer, len(tg.questions))
		inTurn(len(tg.questions), r.asks, func(n int, user int64) { res.Answers[n] = r.answer(ctx, id, user, n, tg.questions[n], fail) })
	}
	return res
}

// inTurn runs ask on n questions in order, users at a time, each by a user
// (1 to users) that no other question has meanwhile: chat lets a user write
// but two answers at a time.
func inTurn(n, users int, ask func(n int, user int64)) {
	free, wg := make(chan int64, users), sync.WaitGroup{}
	for u := range users {
		free <- int64(u + 1)
	}
	for i := range n {
		user := <-free
		wg.Go(func() { defer func() { free <- user }(); ask(i, user) })
	}
	wg.Wait()
}

// answer asks q, the n-th question, as user, and scores its answer: the
// keywords it lacks and the judge's verdict.
func (r *rig) answer(ctx context.Context, id, user int64, n int, q question, fail func(string, error)) answer {
	a := r.ask(ctx, id, user, q.ask)
	low := strings.ToLower(a.Answer)
	for _, w := range q.want {
		if !slices.ContainsFunc(strings.Split(strings.ToLower(w), "|"), func(alt string) bool { return strings.Contains(low, alt) }) {
			a.Missing = append(a.Missing, w)
		}
	}
	if len(a.Trace.Dropped) > 0 { // the gathering's budget keeps all it gathered within what Format shows
		fail(fmt.Sprintf("question %d", n+1), fmt.Errorf("its evidence dropped %d hits gathered", len(a.Trace.Dropped)))
	}
	if a.Error != "" {
		fail(fmt.Sprintf("question %d", n+1), errors.New(a.Error))
	} else if v, err := judge(ctx, r.judge, a.Answer); err != nil {
		fail(fmt.Sprintf("judging question %d", n+1), err)
	} else {
		a.SelfTalk, a.SelfTalkSentence, a.Language, a.OtherLanguage = v.SelfTalk, v.Sentence, v.Language, !speaks(v.Language, q.language())
	}
	return a
}

// writeWiki writes the wiki of the snapshot at sha and measures it.
func (r *rig) writeWiki(ctx context.Context, id int64, sha string, fail func(string, error)) *wikiRun {
	r.meter.take()
	r.last = &wikiRun{Touched: map[string]int{}}
	start := time.Now()
	err := r.wiki.Run(ctx, id, sha, jobs.Progress{})
	r.last.Seconds, r.last.Failed = since(start), err != nil
	if err != nil {
		fail("wiki "+sha[:7], err)
	}
	calls, u, _ := r.meter.take()
	r.last.Calls, r.last.InTokens, r.last.OutTokens, r.last.InChars, r.last.OutChars, r.last.CachedTokens, r.last.EstInTokens, r.last.EstOutTokens = calls, u.InTokens, u.OutTokens, u.InChars, u.OutChars, u.CachedTokens, u.EstIn, u.EstOut
	return r.last
}

// ask asks q as user would and times it (trace.Clock), keeping what its evidence
// event tells of the search: its rounds, and the gathering's calls and stop,
// the trace of its events, the lines its citations cover, and each of its
// model requests' usage.
func (r *rig) ask(ctx context.Context, id, user int64, q string) answer {
	a, start, c := answer{Question: q, Trace: &trace.Trace{}}, time.Now(), trace.Clock{}
	ctx = context.WithValue(ctx, asking{}, &a)
	err := r.chat.Ask(ctx, chat.Request{RepoID: id, Question: q, UserID: user}, func(event string, data any) {
		at := time.Since(start)
		c.Tick(event, at)
		a.Trace.Take(event, data, at)
		if d, ok := data.(map[string]any); ok && event == "evidence" {
			a.Rounds, _ = d["rounds"].(int)
			a.Calls, _ = d["calls"].(map[string]int)
			a.Stop, _ = d["stop"].(string)
		} else if m, ok := data.(chat.Message); ok && event == "done" {
			a.Answer, a.Citations, a.Lines = m.Content, len(m.Citations), score.Lines(m.Citations)
		}
	})
	first, begun, rewrites := c.Read()
	a.FirstTokenSeconds, a.AnswerStartSeconds, a.Rewrites, a.Seconds = first.Seconds(), begun.Seconds(), rewrites, since(start)
	a.Requests = r.meter.requests(&a)
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
