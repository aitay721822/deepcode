package bench

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type report struct {
	Started         string   `json:"started"`
	Deepcode        string   `json:"deepcode"` // the commit measured
	Model           string   `json:"model"`
	ReasoningEffort string   `json:"reasoningEffort"`
	Temperature     string   `json:"temperature"`
	Repos           []result `json:"repos"`
}

type result struct {
	Repo string `json:"repo"`
	URL  string `json:"url"`
	Base string `json:"base"`
	Head string `json:"head"`
	// The head snapshot, after the update.
	Files         int `json:"files"`
	Generated     int `json:"generatedFiles"`
	Languages     int `json:"languages"`
	Chunks        int `json:"chunks"`
	Symbols       int `json:"symbols"`
	Edges         int `json:"importEdges"`
	DiagramEdges  int `json:"diagramEdges"` // the arrows the overview's diagram draws
	Pages         int `json:"pages"`
	PageCitations int `json:"pageCitations"`

	IndexFailed  bool      `json:"indexFailed"`  // the base was not indexed: nothing else was measured
	IndexSeconds float64   `json:"indexSeconds"` // the first index, at base
	Wiki         *wikiRun  `json:"wiki"`         // the first wiki, at base: its last run
	WikiRuns     []wikiRun `json:"wikiRuns"`     // each run of it, by pages written at a time
	Tree         []string  `json:"pageTree"`     // head's wiki, as a Markdown list
	Topics       *coverage `json:"topicCoverage"`
	Update       struct {
		Failed         bool     `json:"failed"` // the head was not indexed: the rest is empty
		ChangedFiles   int      `json:"changedFiles"`
		IndexSeconds   float64  `json:"indexSeconds"`
		Wiki           *wikiRun `json:"wiki"`
		PagesRewritten int      `json:"pagesRewritten"`
	} `json:"update"` // base to head
	Eval    *evalResult `json:"eval"`
	Answers []answer    `json:"answers"`
	Errors  []string    `json:"errors"`
	Failed  []string    `json:"failedSteps"` // the steps of Errors: what they leave uncounted reads failed
}

// wikiRun is one wiki generation; the tokens are what the server reported, 0
// when it reports none (then the characters stand in). A failed run's
// numbers are what it did before it stopped.
type wikiRun struct {
	Parallel  int     `json:"parallel"` // pages written at a time
	Failed    bool    `json:"failed"`
	Seconds   float64 `json:"seconds"`
	Pages     int     `json:"pages"`
	Calls     int     `json:"modelCalls"`
	InTokens  int     `json:"inputTokens"`
	OutTokens int     `json:"outputTokens"`
	InChars   int     `json:"inputChars"`
	OutChars  int     `json:"outputChars"`
	// CachedTokens are the input tokens the server read from its prompt cache.
	CachedTokens int `json:"cachedTokens"`
}

type evalResult struct {
	Questions int      `json:"questions"`
	Recall    float64  `json:"recallAt10"`
	MRR       float64  `json:"mrr"`
	Misses    []string `json:"misses"`
}

type answer struct {
	Question          string   `json:"question"`
	FirstTokenSeconds float64  `json:"firstTokenSeconds"`
	Seconds           float64  `json:"seconds"`
	Citations         int      `json:"citations"`
	Missing           []string `json:"missingKeywords"` // the words it must hold and does not
	SelfTalk          bool     `json:"selfTalk"`        // it thinks aloud
	OtherLanguage     bool     `json:"otherLanguage"`   // not in the question's language
	Answer            string   `json:"answer"`
	Error             string   `json:"error,omitempty"`
}

// write saves the report as latest.json and latest.md in dir, and the JSON
// again under runs/ by its start, so batches can be compared.
func (rep report) write(dir string) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "runs"), 0o755); err != nil {
		return err
	}
	stamp := strings.NewReplacer(":", "", "-", "").Replace(rep.Started)
	for _, f := range []struct {
		name    string
		content []byte
	}{{filepath.Join("runs", stamp+".json"), data}, {"latest.json", data}, {"latest.md", []byte(rep.markdown())}} {
		// Written aside and renamed, so a stop midway leaves the last whole file.
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p+".tmp", f.content, 0o644); err != nil {
			return err
		}
		if err := os.Rename(p+".tmp", p); err != nil {
			return err
		}
	}
	return nil
}

func (rep report) markdown() string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	p("# deepcode bench\n\nStarted %s · deepcode %s · model %s · reasoning effort %q · temperature %q\n", rep.Started, rep.Deepcode, rep.Model, rep.ReasoningEffort, rep.Temperature)
	p("The first index's time includes fetching the repository from GitHub. A repository with errors (listed below the tables) has numbers that are partial.\n")
	p("## Index and graph (head)\n\n| Repository | Files | Generated | Languages | Chunks | Symbols | Import edges | Diagram edges | First index | Errors |\n|---|---|---|---|---|---|---|---|---|---|")
	for _, r := range rep.Repos {
		index := fmt.Sprintf("%.1f s", r.IndexSeconds)
		if r.IndexFailed {
			index = "failed"
		}
		p("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %d |", r.Repo, atHead(r, "files", r.Files), atHead(r, "generated files", r.Generated),
			atHead(r, "languages", r.Languages), atHead(r, "chunks", r.Chunks), atHead(r, "symbols", r.Symbols), atHead(r, "edges", r.Edges),
			atHead(r, "diagram", r.DiagramEdges), index, len(r.Errors))
	}
	p("\n## First wiki (base)\n\nTime by pages written at a time; the rest is the last run's.\n\n| Repository | Time | Pages | Model calls | Input tokens | Cached input | Output tokens | Input chars | Output chars | Page citations (head) |\n|---|---|---|---|---|---|---|---|---|---|")
	for _, r := range rep.Repos {
		if w := r.Wiki; w != nil {
			times := make([]string, len(r.WikiRuns))
			for i, run := range r.WikiRuns {
				times[i] = fmt.Sprintf("%d at a time: %s", run.Parallel, took(&run))
			}
			cached := "–"
			if w.InTokens > 0 {
				cached = fmt.Sprintf("%d (%d%%)", w.CachedTokens, 100*w.CachedTokens/w.InTokens)
			}
			p("| %s | %s | %s | %d | %s | %s | %s | %d | %d | %s |", r.Repo, cmp.Or(strings.Join(times, " · "), took(w)), count(r, "base pages", w.Pages), w.Calls,
				tokens(w.InTokens), cached, tokens(w.OutTokens), w.InChars, w.OutChars, atHead(r, "page citations", r.PageCitations))
		}
	}
	p("\n## Update by one commit (base → head)\n\n| Repository | Changed files | Index | Wiki | Pages rewritten | Model calls |\n|---|---|---|---|---|---|")
	for _, r := range rep.Repos {
		wiki, pages, calls := "–", "–", "–"
		if w := r.Update.Wiki; w != nil {
			rewritten := count(r, "pages rewritten", r.Update.PagesRewritten)
			if r.Wiki == nil || r.Wiki.Failed || w.Failed {
				rewritten = "–"
			}
			wiki, pages, calls = took(w), rewritten+" of "+count(r, "head pages", w.Pages), fmt.Sprint(w.Calls)
		}
		changed, index := atHead(r, "changed files", r.Update.ChangedFiles), fmt.Sprintf("%.1f s", r.Update.IndexSeconds)
		if r.Update.Failed {
			index = "failed"
		}
		p("| %s | %s | %s | %s | %s | %s |", r.Repo, changed, index, wiki, pages, calls)
	}
	p("\n## Topic coverage (head)\n\nDeepWiki's topics a page title or \"##\" or \"###\" heading names, a word of a name like WelcomeController counting; test and tooling pages cite only tests, CI, manifests or editor settings.\n\n| Repository | Covered | Pages | Test and tooling pages | Missing |\n|---|---|---|---|---|")
	for _, r := range rep.Repos {
		if c := r.Topics; c != nil {
			p("| %s | %d of %d (%d%%) | %d | %d (%d%%) | %s |", r.Repo, c.Covered, c.Topics, 100*c.Covered/max(c.Topics, 1), r.Pages,
				c.ToolingPages, 100*c.ToolingPages/max(r.Pages, 1), cmp.Or(strings.Join(c.Missing, ", "), "–"))
		}
	}
	p("\n## Retrieval eval (head)\n\n| Repository | Questions | recall@10 | MRR |\n|---|---|---|---|")
	for _, r := range rep.Repos {
		if e := r.Eval; e != nil {
			p("| %s | %d | %.3f | %.3f |", r.Repo, e.Questions, e.Recall, e.MRR)
		}
	}
	var asked, passed, uncited, talking, foreign int
	for _, r := range rep.Repos {
		for _, a := range r.Answers {
			asked++
			if a.Error == "" && len(a.Missing) == 0 {
				passed++
			}
			if a.Error == "" && a.Citations == 0 {
				uncited++
			}
			if a.SelfTalk {
				talking++
			}
			if a.OtherLanguage {
				foreign++
			}
		}
	}
	p("\n## Questions (head)\n\n%d of %d answers hold their keywords; %d cite nothing; %d think aloud; %d answer in another language.\n", passed, asked, uncited, talking, foreign)
	p("| Repository | Question | First token | Total | Citations | Keywords | Self-talk | Language |\n|---|---|---|---|---|---|---|---|")
	for _, r := range rep.Repos {
		for _, a := range r.Answers {
			first, total, cites, keys, talk, lang := fmt.Sprintf("%.1f s", a.FirstTokenSeconds), fmt.Sprintf("%.1f s", a.Seconds), fmt.Sprint(a.Citations), "pass", "no", "same"
			if len(a.Missing) > 0 {
				keys = "fail: no " + strings.Join(a.Missing, ", ")
			}
			if a.SelfTalk {
				talk = "yes"
			}
			if a.OtherLanguage {
				lang = "other"
			}
			if a.Error != "" {
				first, total, cites, keys, lang = "failed", fmt.Sprintf("failed after %.1f s", a.Seconds), "–", "fail", "–"
			}
			p("| %s | %s | %s | %s | %s | %s | %s | %s |", r.Repo, a.Question, first, total, cites, keys, talk, lang)
		}
	}
	for _, r := range rep.Repos {
		if len(r.Errors) > 0 {
			p("\n**%s errors:** %s", r.Repo, strings.Join(r.Errors, "; "))
		}
	}
	p("\n## Page tree (head)")
	for _, r := range rep.Repos {
		if len(r.Tree) > 0 {
			p("\n### %s (%d pages)\n\n%s", r.Repo, len(r.Tree), strings.Join(r.Tree, "\n"))
		}
	}
	p("\n## Answers")
	for _, r := range rep.Repos {
		for _, a := range r.Answers {
			text := a.Answer
			if a.Error != "" {
				text = "Error: " + a.Error
			}
			p("\n### %s: %s\n\n%s", r.Repo, a.Question, text)
		}
	}
	return b.String()
}

// count shows n, or "failed" when step, the query that counts it, failed.
func count(r result, step string, n int) string {
	if slices.Contains(r.Failed, step) {
		return "failed"
	}
	return fmt.Sprint(n)
}

// atHead is count for the head snapshot, which a failed update leaves uncounted.
func atHead(r result, step string, n int) string {
	if r.Update.Failed {
		return "failed"
	}
	return count(r, step, n)
}

// took shows how long a wiki run took, and if it failed.
func took(w *wikiRun) string {
	if w.Failed {
		return fmt.Sprintf("failed after %.0f s", w.Seconds)
	}
	return fmt.Sprintf("%.0f s", w.Seconds)
}

// tokens shows a token count, or that the server reported none.
func tokens(n int) string {
	if n == 0 {
		return "–"
	}
	return fmt.Sprint(n)
}

// TestMarkdown: failed steps read as failed, never as numbers; answers show
// the keywords they miss and whether they think aloud, the first wiki its time
// at each number of pages at a time, and head's wiki its tree; the report
// lands whole, with no file left aside.
func TestMarkdown(t *testing.T) {
	ok, broken := result{Repo: "ok", Wiki: &wikiRun{Seconds: 10, Pages: 6, InTokens: 200, CachedTokens: 150}}, result{Repo: "broken", Wiki: &wikiRun{Failed: true, Seconds: 3}}
	ok.Update.Wiki, ok.Update.PagesRewritten = &wikiRun{Seconds: 5, Pages: 6}, 1
	broken.Update.Failed, broken.Errors = true, []string{"update index: boom"}
	ok.Failed = []string{"symbols", "pages rewritten"} // a count whose query failed
	broken.Answers = []answer{{Question: "q", Error: "timeout"}}
	ok.Answers = []answer{{Question: "k", Citations: 2, Missing: []string{"Mount"}, SelfTalk: true, OtherLanguage: true}}
	ok.WikiRuns, ok.Tree = []wikiRun{{Parallel: 1, Seconds: 20}, {Parallel: 2, Seconds: 12}}, []string{"- 1 Overview", "- 2 Core", "  - 2.1 Mux"}
	ok.Pages, ok.Topics = 10, &coverage{Topics: 4, Covered: 3, Missing: []string{"SSE"}, ToolingPages: 2}
	md := report{Repos: []result{ok, broken}}.markdown()
	for _, want := range []string{"| ok | 0 | 0.0 s | 5 s | failed of 6 |", "| ok | 0 | 0 | 0 | 0 | failed | 0 | 0 | 0.0 s | 0 |", "failed after 3 s", "| broken | failed | failed |", "| broken | failed | failed | failed | failed | failed | failed | failed | 0.0 s | 1 |", "| broken | q | failed | failed after 0.0 s | – | fail | no | – |", "update index: boom", "Error: timeout",
		"| ok | 1 at a time: 20 s · 2 at a time: 12 s | 6 | 0 | 200 | 150 (75%) | – |", "| broken | failed after 3 s | 0 | 0 | – | – | – |", "| ok | k | 0.0 s | 0.0 s | 2 | fail: no Mount | yes | other |", "### ok (3 pages)\n\n- 1 Overview\n- 2 Core\n  - 2.1 Mux",
		"0 of 2 answers hold their keywords; 0 cite nothing; 1 think aloud; 1 answer in another language.", "| ok | 3 of 4 (75%) | 10 | 2 (20%) | SSE |"} {
		if !strings.Contains(md, want) {
			t.Errorf("the report lacks %q:\n%s", want, md)
		}
	}
	dir := t.TempDir()
	if err := (report{Started: "2026-11-07T00:00:00Z"}).write(dir); err != nil {
		t.Fatal(err)
	}
	tmp, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	runsTmp, _ := filepath.Glob(filepath.Join(dir, "runs", "*.tmp"))
	tmp = append(tmp, runsTmp...)
	if _, err := os.Stat(filepath.Join(dir, "runs", "20261107T000000Z.json")); err != nil || len(tmp) > 0 {
		t.Fatalf("runs file: %v; left aside: %q", err, tmp)
	}
}
