package probe

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"deepcode/cmd/deepcode/config"
	"deepcode/internal/app"
	"deepcode/internal/knowledge/chat"
	"deepcode/internal/knowledge/chat/background"
	"deepcode/internal/knowledge/chat/harness"
	chatstore "deepcode/internal/knowledge/chat/store"
	"deepcode/internal/knowledge/retrieval"
	retrievalstore "deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/knowledge/wiki"
	wikistore "deepcode/internal/knowledge/wiki/store"
	"deepcode/internal/platform/cache"
	"deepcode/internal/platform/jobs"
	jobstore "deepcode/internal/platform/jobs/store"
	"deepcode/internal/repo/catalog"
	catalogstore "deepcode/internal/repo/catalog/store"
	"deepcode/internal/repo/index/graph"
	graphstore "deepcode/internal/repo/index/graphstore"
	"deepcode/internal/repo/index/indexer/vectorstore"
	"deepcode/test/bench/score"
)

// langCase is one question of Probe L: its repository, its group (L1 asked
// alone, L2 after a turn in Chinese, L3 a deep answer beside a page of a wiki
// written in Chinese) and the language its answer must be written in.
type langCase struct{ repo, group, ask, want string }

// langCases are the bench's English, German and Japanese questions on chi and
// eShop (test/bench/cases_test.go): L1 asks every question, L2 the English and
// German ones, L3 chi's.
var langCases = func() []langCase {
	qs := map[string][][2]string{
		"chi": {{"How does chi's router match a request path to a handler, including URL parameters?", "English"},
			{"How do middlewares work in chi, and how are they chained with Use, With and Group?", "English"},
			{"Which middlewares does chi ship, and how does the Timeout middleware work?", "English"},
			{"Wie funktionieren Middlewares in chi, und wie werden sie mit Use, With und Group verkettet?", "German"},
			{"Welche Middlewares liefert chi mit, und wie funktioniert die Timeout-Middleware?", "German"},
			{"chi のルーターは、URL パラメータを含め、リクエストのパスをどのようにハンドラーに対応付けますか？", "Japanese"},
			{"chi でサブルーターを Mount すると、ルーティングのコンテキストはどのようにそこへ渡されますか？", "Japanese"}},
		"eshop": {{"Which services make up eShop, and how does .NET Aspire orchestrate them?", "English"},
			{"How do the services communicate through integration events and the event bus?", "English"},
			{"How is the catalog's semantic search with AI embeddings implemented?", "English"},
			{"Wie kommunizieren die Dienste über Integrationsereignisse und den Event-Bus miteinander?", "German"},
			{"Wie ist die semantische Suche des Katalogs mit KI-Embeddings umgesetzt?", "German"},
			{"eShop はどのサービスで構成され、.NET Aspire はそれらをどのように編成していますか？", "Japanese"},
			{"Ordering サービスは、バスケットのチェックアウトから支払いまで、注文をどのように処理しますか？", "Japanese"}},
	}
	var out []langCase
	for _, group := range []string{"L1", "L2", "L3"} {
		for _, repo := range []string{"chi", "eshop"} {
			for _, q := range qs[repo] {
				if group == "L1" || q[1] != "Japanese" && (group == "L2" || repo == "chi") {
					out = append(out, langCase{repo, group, q[0], q[1]})
				}
			}
		}
	}
	return out
}()

// chineseTurn is the turn in Chinese an L2 question follows, per repository:
// a question and its answer, stored as the conversation so far.
var chineseTurn = map[string][2]string{
	"chi": {"chi 的路由器如何把請求路徑對應到處理函式？",
		"chi 的 `Mux` 把路由存在基數樹（radix tree）裡。`ServeHTTP` 收到請求後，`routeHTTP` 以 `findRoute` 逐段比對路徑，URL 參數記在路由 `Context` 的 `URLParams`，處理函式再以 `chi.URLParam` 讀取。"},
	"eshop": {"eShop 由哪些服務組成？",
		"eShop 以 .NET Aspire 的 `eShop.AppHost` 編排各個服務：Catalog、Basket、Ordering、Identity 與 Webhooks 等 API，以及 WebApp 前端；服務之間透過 RabbitMQ 事件匯流排傳遞整合事件。"},
}

// langRun is one answer's measure, logged as a "PROBE-L" line of JSON.
type langRun struct {
	Arm     string  `json:"arm"`
	Run     int     `json:"run"`
	Repo    string  `json:"repo"`
	Group   string  `json:"group"`
	Ask     string  `json:"ask"`
	Want    string  `json:"want"`
	Got     string  `json:"got"`  // by script, else the judge's
	By      string  `json:"by"`   // "script" or "judge"
	Page    string  `json:"page"` // L3: the wiki page beside the answer
	OK      bool    `json:"ok"`
	Seconds float64 `json:"seconds"`
	First   float64 `json:"first"` // seconds to the answer's first delta
	Answer  string  `json:"answer"`
	Error   string  `json:"error,omitempty"`
}

// TestLanguage is Probe L: the language each answer is written in, against
// its question's, on one bench repository's base snapshot. DEEPCODE_PROBE=L,
// DEEPCODE_PROBE_REPO (chi or eshop), DEEPCODE_PROBE_DIR (the indexed
// snapshots, kept between runs), DEEPCODE_EVAL_GIT holding the bare clone,
// DEEPCODE_PROBE_GROUPS (the groups asked, as "L1,L2": chi's deep L3 is a
// test of its own, as chi's 17 answers do not fit 10 minutes), DEEPCODE_PROBE_ARM
// and DEEPCODE_PROBE_RUN (labels), DEEPCODE_CHAT_* and DEEPCODE_EMBED_* as serve
// reads them: hybrid search, as the bench's. With DEEPCODE_PROBE_SETUP=1 it
// only indexes the snapshot, its vectors too, and, on chi, writes its wiki in
// Traditional Chinese, at most 8 pages, for L3: so a run's 10 minutes go to
// its answers.
func TestLanguage(t *testing.T) {
	if os.Getenv("DEEPCODE_PROBE") != "L" {
		t.Skip("set DEEPCODE_PROBE=L to probe the language answers are written in")
	}
	name, dir, arm := os.Getenv("DEEPCODE_PROBE_REPO"), os.Getenv("DEEPCODE_PROBE_DIR"), os.Getenv("DEEPCODE_PROBE_ARM")
	repo, ok := planRepos[name]
	run, err := strconv.Atoi(os.Getenv("DEEPCODE_PROBE_RUN"))
	setup, groups := os.Getenv("DEEPCODE_PROBE_SETUP") == "1", strings.Split(os.Getenv("DEEPCODE_PROBE_GROUPS"), ",")
	var cases []langCase
	for _, c := range langCases {
		if c.repo == name && slices.Contains(groups, c.group) {
			cases = append(cases, c)
		}
	}
	if !ok || chineseTurn[name][0] == "" || err != nil || run < 1 || dir == "" || arm == "" || os.Getenv("DEEPCODE_EVAL_GIT") == "" || !setup && len(cases) == 0 {
		t.Fatal("set DEEPCODE_PROBE_REPO to chi or eshop, DEEPCODE_PROBE_RUN to 1 or more, DEEPCODE_PROBE_ARM, DEEPCODE_PROBE_DIR, DEEPCODE_EVAL_GIT and DEEPCODE_PROBE_GROUPS to groups it has")
	}
	cfg, _, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	model, embed := app.Providers(cfg)
	tokens, err := harness.LoadTokens(os.Getenv("DEEPCODE_CHAT_TOKENIZER"))
	if model == nil || embed == nil || err != nil {
		t.Fatalf("the probe needs DEEPCODE_CHAT_MODEL, DEEPCODE_EMBED_MODEL (the bench's hybrid search) and a tokenizer: %v", err)
	}
	ctx := context.Background()
	db, snap := indexed(t, filepath.Join(dir, name), filepath.Join(os.Getenv("DEEPCODE_EVAL_GIT"), name+".git"), repo.owner, name, repo.base, embed)
	if _, err := db.Exec(`INSERT OR IGNORE INTO users(id, username, password_hash) VALUES (1, 'probe', '')`); err != nil {
		t.Fatal(err)
	}
	graphs := &graph.Graph{Store: &graphstore.Store{DB: db}}
	cat := &catalog.Catalog{Store: &catalogstore.Store{DB: db}, Queue: &jobs.Queue{Store: &jobstore.Store{DB: db}}}
	ret := &retrieval.Retriever{Store: &retrievalstore.Store{DB: db}, Graph: graphs, Embed: embed, Vectors: &vectorstore.SQLite{DB: db}, Cache: cache.New(1 << 20)}
	if setup {
		if name == "chi" {
			g := &wiki.Generator{Store: &wikistore.Store{DB: db}, Graph: graphs, Catalog: cat, Retriever: ret, Chat: model,
				Language: "Traditional Chinese (Taiwan)", MaxPages: 8, Parallel: 1}
			start := time.Now()
			if err := g.Run(ctx, 1, repo.base, jobs.Progress{}); err != nil {
				t.Fatal(err)
			}
			t.Logf("PROBE-L-WIKI %s written in %.0f s", name, time.Since(start).Seconds())
		}
		return
	}
	store := &chatstore.Store{DB: db}
	svc := &chat.Service{Store: store, Catalog: cat, Retriever: ret, Chat: model, Tokens: tokens}
	var vectors int
	if err := db.QueryRow(`SELECT count(*) FROM chunk_embeddings`).Scan(&vectors); err != nil || vectors == 0 {
		t.Fatalf("no vectors in %s (%v): index it with DEEPCODE_EMBED_MODEL set, in a directory of its own (Probe C's have none)", dir, err)
	}
	titles, contents, err := store.WikiPages(ctx, 1, snap)
	if err != nil {
		t.Fatal(err)
	}
	page := map[string]string{} // L3's question: the page of chi's wiki in Chinese beside its answer
	for _, c := range cases {
		if c.group == "L3" {
			i := background.Nearest(c.ask, titles, contents)
			if i < 0 || !strings.ContainsFunc(contents[i], isHanRune) {
				t.Fatalf("%q has no page of a wiki in Chinese beside it: write chi's with DEEPCODE_PROBE_SETUP=1", c.ask)
			}
			page[c.ask] = titles[i]
		}
	}
	for _, c := range cases {
		res := langRun{Arm: arm, Run: run, Repo: name, Group: c.group, Ask: c.ask, Want: c.want, Page: page[c.ask]}
		req := chat.Request{RepoID: 1, Question: c.ask, UserID: 1, Deep: c.group == "L3"}
		if c.group == "L2" {
			conv, err := store.Start(ctx, 1, snap, chineseTurn[name][0], 1)
			for i, role := range []string{"user", "assistant"} {
				if err == nil {
					_, err = store.AddMessage(ctx, conv, role, chineseTurn[name][i], nil)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			req.ConversationID = conv
		}
		start := time.Now()
		err := svc.Ask(ctx, req, func(event string, data any) {
			if event == "delta" && res.First == 0 {
				res.First = time.Since(start).Round(100 * time.Millisecond).Seconds()
			}
			if m, ok := data.(chat.Message); ok && event == "done" {
				res.Answer = m.Content
			}
		})
		res.Seconds = time.Since(start).Round(100 * time.Millisecond).Seconds()
		if err != nil {
			res.Error = err.Error()
		} else if res.Got, res.By = written(res.Answer), "script"; res.Got == "" {
			v, err := score.Judge(ctx, model, res.Answer)
			res.Got, res.By = v.Language, "judge"
			if err != nil {
				res.Error = "judge: " + err.Error()
			}
		}
		res.OK = res.Error == "" && score.Speaks(res.Got, c.want)
		b, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("PROBE-L %s", b)
	}
}

var (
	// proseOnly drops what an answer holds as code, links or citations.
	proseOnly = regexp.MustCompile("(?s)```.*?```|```.*|`[^`\n]*`|\\[[^\\]\n]*\\](\\([^)\n]*\\))?")
	latinWord = regexp.MustCompile(`[A-Za-z]+`)
)

func isHanRune(r rune) bool { return unicode.Is(unicode.Han, r) }

// written is the language an answer's prose is written in where its script
// tells: Japanese with any kana, Chinese with at least as many Han characters
// as Latin words; "" for the judge to read otherwise.
func written(answer string) string {
	prose := proseOnly.ReplaceAllString(answer, " ")
	if strings.ContainsFunc(prose, func(r rune) bool { return unicode.In(r, unicode.Hiragana, unicode.Katakana) }) {
		return "Japanese"
	}
	han, latin := 0, len(latinWord.FindAllStringIndex(prose, -1))
	for _, r := range prose {
		if isHanRune(r) {
			han++
		}
	}
	if han > 0 && han >= latin {
		return "Chinese"
	}
	return ""
}

// TestWritten: kana make an answer Japanese, Han as many as its Latin words or
// more Chinese, code and citations aside; any other is left to the judge. The
// groups ask what langCases says.
func TestWritten(t *testing.T) {
	for s, want := range map[string]string{"chi のルーターは `findRoute` を使います。": "Japanese", "路由器以 `findRoute` 比對路徑 [tree.go:1-9](x)。": "Chinese",
		"The router calls `findRoute` [tree.go:1-9](x).": "", "Der Router ruft `路由` auf.": "", "": "",
		"路由 is fast": "Chinese", "路 is fast": "", "```go\nfunc 路由() {}\n```\nThe router.": "", "```go\nx := 1\n```\n路由器很快。": "Chinese", "見 [src/very/long/path/name.go:1-9](https://x/a/b/c)。": "Chinese"} {
		if got := written(s); got != want {
			t.Errorf("written(%q) = %q, want %q", s, got, want)
		}
	}
	n := map[string]int{}
	for _, c := range langCases {
		n[c.group+" "+c.repo+" "+c.want]++
	}
	want := map[string]int{"L1 chi English": 3, "L1 chi German": 2, "L1 chi Japanese": 2, "L1 eshop English": 3, "L1 eshop German": 2, "L1 eshop Japanese": 2,
		"L2 chi English": 3, "L2 chi German": 2, "L2 eshop English": 3, "L2 eshop German": 2, "L3 chi English": 3, "L3 chi German": 2}
	if !maps.Equal(n, want) {
		t.Errorf("groups %v, want %v", n, want)
	}
}
