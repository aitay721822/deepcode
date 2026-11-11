package bench

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/knowledge/chat/loop"
	"deepcode/internal/knowledge/wiki"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/platform/ai"
	aicontract "deepcode/internal/platform/ai/contract"
	"deepcode/test/bench/shape"
	"deepcode/test/bench/trace"
)

type report struct {
	Started         string   `json:"started"`
	Deepcode        string   `json:"deepcode"` // the commit measured
	Model           string   `json:"model"`
	ReasoningEffort string   `json:"reasoningEffort"`
	Temperature     string   `json:"temperature"`
	EmbedModel      string   `json:"embedModel,omitempty"` // "": lexical search only
	Judged          bool     `json:"judged,omitempty"`     // its selfTalk and language are the judge's (judge_test.go); an older run's selfTalk is a detector's
	Repos           []result `json:"repos"`
}

type result struct {
	Repo string `json:"repo"`
	URL  string `json:"url"`
	Base string `json:"base"`
	Head string `json:"head"`
	// The head snapshot, after the update.
	Files         int          `json:"files"`
	Generated     int          `json:"generatedFiles"`
	Languages     int          `json:"languages"`
	Chunks        int          `json:"chunks"`
	Symbols       int          `json:"symbols"`
	Edges         int          `json:"importEdges"`
	DiagramEdges  int          `json:"diagramEdges"` // the arrows the overview's diagram draws
	Pages         int          `json:"pages"`
	PageCitations int          `json:"pageCitations"`
	CitationSpan  citationSpan `json:"citationSpan"` // lines per page citation

	IndexFailed  bool         `json:"indexFailed"`  // the base was not indexed: nothing else was measured
	IndexSeconds float64      `json:"indexSeconds"` // the first index, at base
	Wiki         *wikiRun     `json:"wiki"`         // the first wiki, at base: its last run
	WikiRuns     []wikiRun    `json:"wikiRuns"`     // each run of it, by pages written at a time
	Tree         []string     `json:"pageTree"`     // head's wiki, as a Markdown list
	Topics       *coverage    `json:"topicCoverage"`
	Plan         *shape.Shape `json:"plan"`          // head's wiki's
	DeepWiki     int          `json:"deepWikiPages"` // DeepWiki's page count
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
	// Planning is how the model's plan of the pages went, nil when they were carried over.
	Planning *wiki.Planning `json:"planning,omitempty"`
}

type evalResult struct {
	Questions int      `json:"questions"`
	Recall    float64  `json:"recallAt10"`
	MRR       float64  `json:"mrr"`
	Misses    []string `json:"misses"`
}

type answer struct {
	Question           string         `json:"question"`
	FirstTokenSeconds  float64        `json:"firstTokenSeconds"`
	AnswerStartSeconds float64        `json:"answerStartSeconds"` // the first words of the draft it ended with
	Seconds            float64        `json:"seconds"`
	Rewrites           int            `json:"rewrites"`        // drafts written again after the first
	Rounds             int            `json:"rounds"`          // its searches, the first counting
	Calls              map[string]int `json:"calls,omitempty"` // the gathering's calls, by tool
	Stop               string         `json:"stop,omitempty"`  // the rule that ended the gathering
	Citations          int            `json:"citations"`
	Missing            []string       `json:"missingKeywords"` // the words it must hold and does not
	SelfTalk           bool           `json:"selfTalk"`        // it talks to itself, as the judge reads it
	SelfTalkSentence   string         `json:"selfTalkSentence,omitempty"`
	Language           string         `json:"language,omitempty"` // as the judge names it; "": not judged
	OtherLanguage      bool           `json:"otherLanguage"`      // not in the question's language
	Answer             string         `json:"answer"`
	Error              string         `json:"error,omitempty"`
	Trace              *trace.Trace   `json:"trace,omitempty"` // how it was gathered and written; nil in older runs
	// Requests are its model requests' usage, in order: a terms rewrite's when
	// there was one, the gathering's replies, then the answer's, which an
	// answer with no evidence does not make. Nil in older runs.
	Requests []ai.Usage `json:"requests,omitempty"`
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
	p("# deepcode bench\n\nStarted %s · deepcode %s · model %s · reasoning effort %q · temperature %q · embeddings %s\n", rep.Started, rep.Deepcode, rep.Model, rep.ReasoningEffort, rep.Temperature, cmp.Or(rep.EmbedModel, "none"))
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
	p("\n## First wiki (base)\n\nTime by pages written at a time; the rest is the last run's. Lines per citation: the median and the most a page citation spans. Planning: its time and requests.\n\n| Repository | Time | Pages | Model calls | Input tokens | Cached input | Output tokens | Input chars | Output chars | Page citations (head) | Lines per citation (head) | Planning |\n|---|---|---|---|---|---|---|---|---|---|---|---|")
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
			lines := fmt.Sprintf("%d, at most %d", r.CitationSpan.Median, r.CitationSpan.Max)
			if atHead(r, "citation span", 0) == "failed" || atHead(r, "page citations", 0) == "failed" { // no count to read "–" from
				lines = "failed"
			} else if r.PageCitations == 0 {
				lines = "–" // no citation to measure
			}
			planning := "–" // no model planned it
			if s := w.Planning; s != nil {
				planning = fmt.Sprintf("%.1f s, %d requests", s.Seconds, s.Requests)
			}
			p("| %s | %s | %s | %d | %s | %s | %s | %d | %d | %s | %s | %s |", r.Repo, cmp.Or(strings.Join(times, " · "), took(w)), count(r, "base pages", w.Pages), w.Calls,
				tokens(w.InTokens), cached, tokens(w.OutTokens), w.InChars, w.OutChars, atHead(r, "page citations", r.PageCitations), lines, planning)
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
	p("\n## Topic coverage (head)\n\nDeepWiki's topics a page title or \"##\" or \"###\" heading names, a word of a name like WelcomeController counting; test and tooling pages cite only tests, CI, manifests or editor settings; unnamed code files are the code units' files on no page.\n\n| Repository | Covered | Pages | Test and tooling pages | Missing | Unnamed code files |\n|---|---|---|---|---|---|")
	for _, r := range rep.Repos {
		if c := r.Topics; c != nil {
			p("| %s | %d of %d (%d%%) | %d | %d (%d%%) | %s | %s |", r.Repo, c.Covered, c.Topics, 100*c.Covered/max(c.Topics, 1), r.Pages,
				c.ToolingPages, 100*c.ToolingPages/max(r.Pages, 1), cmp.Or(strings.Join(c.Missing, ", "), "–"), listed(c.Unnamed))
		}
	}
	p("\n## Plan\n\nThe head wiki's pages against DeepWiki's count. Thin pages hold files weighing under %d lines, crowded ones over %d. Ceiling and Contracts are the base wiki's plan, which head carries: Ceiling, when it bound, the pages the code's structure wanted; Contracts, per rule the plan broke, how often on the first reply / after its one repair / left for code to fix.\n\n| Repository | Pages | DeepWiki | Thin | Crowded | Projects without a page | Ceiling | Contracts |\n|---|---|---|---|---|---|---|---|",
		place.LinesPerPage, shape.CrowdedPages*place.LinesPerPage)
	for _, r := range rep.Repos {
		if s := r.Plan; s != nil {
			p("| %s | %d | %d | %d | %d | %s | %s |", r.Repo, s.Pages, r.DeepWiki, s.Thin, s.Crowded, listed(s.Homeless), planned(r.Wiki))
		}
	}
	p("\n## Retrieval eval (head)\n\n| Repository | Questions | recall@10 | MRR |\n|---|---|---|---|")
	for _, r := range rep.Repos {
		if e := r.Eval; e != nil {
			p("| %s | %d | %.3f | %.3f |", r.Repo, e.Questions, e.Recall, e.MRR)
		}
	}
	var asked, passed, uncited, talking, foreign, unjudged, evicted, refused, resolved int
	for _, r := range rep.Repos {
		for _, a := range r.Answers {
			asked++
			if tr := a.Trace; tr != nil {
				evicted, refused, resolved = evicted+len(tr.Evicted), refused+len(tr.Refused), resolved+tr.Resolved
			}
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
			if a.Error == "" && a.Language == "" {
				unjudged++
			}
		}
	}
	p("\n## Questions (head)\n\n%d of %d answers hold their keywords; %d cite nothing; %d think aloud; %d answer in another language; %d were not judged. Answer start: the first words of the draft an answer ended with; self-talk and language as the judge reads them. The judge is advisory, hand labels gate; its measured error: %s.\n", passed, asked, uncited, talking, foreign, unjudged, judgeError)
	p("Evidence budget: %d hits evicted, %d refused; %d citation references resolved to a path that differs from the one written by leading folders.\n", evicted, refused, resolved)
	p("| Repository | Question | First token | Answer start | Total | Citations | Keywords | Self-talk | Language |\n|---|---|---|---|---|---|---|---|---|")
	for _, r := range rep.Repos {
		for _, a := range r.Answers {
			first, total, cites, keys, talk, lang := fmt.Sprintf("%.1f s", a.FirstTokenSeconds), fmt.Sprintf("%.1f s", a.Seconds), fmt.Sprint(a.Citations), "pass", "no", "same"
			begun := fmt.Sprintf("%.1f s", a.AnswerStartSeconds)
			if len(a.Missing) > 0 {
				keys = "fail: no " + strings.Join(a.Missing, ", ")
			}
			if a.SelfTalk {
				talk = "yes"
			}
			if a.OtherLanguage {
				lang = "other"
			}
			if a.Error == "" && a.Language == "" {
				talk, lang = "not judged", "not judged"
			}
			if a.Error != "" {
				first, begun, total, cites, keys, lang = "failed", "failed", fmt.Sprintf("failed after %.1f s", a.Seconds), "–", "fail", "–"
			}
			p("| %s | %s | %s | %s | %s | %s | %s | %s | %s |", r.Repo, a.Question, first, begun, total, cites, keys, talk, lang)
		}
	}
	p("\n## Answer time (head)\n\nMeans of the answers that did not fail: the model's replies while gathering; seconds a reply, the gathering's time (the first search's, and a terms rewrite's when there was one, in it) over its replies; the answer's prefill, from the evidence to its first words; its first token; its decode, its output tokens over the seconds from its first words to its end. Prefill and decode are of the answers with evidence.\n\n| Repository | Answers | Replies | Seconds a reply | Answer prefill | First token | Answer decode |\n|---|---|---|---|---|---|---|")
	for _, r := range rep.Repos {
		if row := pace(r.Answers); row != "" {
			p("| %s | %s |", r.Repo, row)
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

// pace is the answer time table's row for one repository's answers, the
// repository's name aside, or "" when none was timed without failing: an
// older run's trace has no times. An answer with no evidence makes no answer
// request: it has no prefill or decode.
func pace(answers []answer) string {
	var n, replies, gathered, perReply, answered, prefill, first, tokens, decoding float64
	for _, a := range answers {
		if a.Error != "" || a.Trace == nil || a.Trace.Gathered <= 0 {
			continue
		}
		n, first = n+1, first+a.FirstTokenSeconds
		if k := float64(a.Rounds - 1); k > 0 { // the first search is a round
			replies += k
			if a.Stop != loop.StopError && a.Stop != loop.StopTime { // else its time holds a reply that failed or was cut, too
				gathered, perReply = gathered+1, perReply+a.Trace.Gathered/k
			}
		}
		if len(a.Trace.Shown) > 0 {
			answered, prefill = answered+1, prefill+a.FirstTokenSeconds-a.Trace.Gathered
			if len(a.Requests) > 0 {
				tokens, decoding = tokens+float64(a.Requests[len(a.Requests)-1].OutTokens), decoding+a.Seconds-a.FirstTokenSeconds
			}
		}
	}
	if n == 0 {
		return ""
	}
	reply, pre, decode := "–", "–", "–"
	if gathered > 0 {
		reply = fmt.Sprintf("%.1f s", perReply/gathered)
	}
	if answered > 0 {
		pre = fmt.Sprintf("%.1f s", prefill/answered)
	}
	if tokens > 0 && decoding > 0 {
		decode = fmt.Sprintf("%.1f tokens/s", tokens/decoding)
	}
	return fmt.Sprintf("%d | %.1f | %s | %s | %.1f s | %s", int(n), replies/n, reply, pre, first/n, decode)
}

// TestPace: an answer with no evidence counts for its replies and first token
// alone, so prefill and decode are of those with evidence; one whose gathering
// an error or the time ended, for those too, not the seconds a reply; an older
// run's, with no times, is left out, and with none left there is no row.
func TestPace(t *testing.T) {
	bare := answer{Rounds: 2, FirstTokenSeconds: 4.1, Seconds: 4.2, Trace: &trace.Trace{Gathered: 4}, Requests: []ai.Usage{{OutTokens: 500}}}
	answered := answer{Rounds: 1, FirstTokenSeconds: 2.9, Seconds: 12.9, Trace: &trace.Trace{Gathered: 1, Shown: make([]trace.Source, 1)}, Requests: []ai.Usage{{OutTokens: 200}}}
	failed := answer{Rounds: 3, Stop: loop.StopError, FirstTokenSeconds: 3.5, Seconds: 3.6, Trace: &trace.Trace{Gathered: 3}}
	cut := answer{Rounds: 2, Stop: loop.StopTime, FirstTokenSeconds: 3.5, Seconds: 3.6, Trace: &trace.Trace{Gathered: 3}}
	old := answer{Rounds: 3, FirstTokenSeconds: 9, Seconds: 20, Trace: &trace.Trace{Shown: make([]trace.Source, 1)}}
	if got := pace([]answer{bare, answered, failed, cut, old}); got != "4 | 1.0 | 4.0 s | 1.9 s | 3.5 s | 20.0 tokens/s" {
		t.Errorf("row %q", got)
	}
	if got := pace([]answer{old}); got != "" {
		t.Errorf("an older run's row %q", got)
	}
}

// TestMeter: a take returns the model's answers since the last, summed and
// each in order, and starts again.
func TestMeter(t *testing.T) {
	var m meter
	m.add(ai.Usage{InTokens: 5, OutTokens: 1})
	m.take()
	m.add(ai.Usage{InTokens: 10, OutTokens: 2})
	m.add(ai.Usage{InTokens: 20, OutTokens: 3, CachedTokens: 4})
	if calls, u, each := m.take(); calls != 2 || u != (ai.Usage{InTokens: 30, OutTokens: 5, CachedTokens: 4}) ||
		!slices.Equal(each, []ai.Usage{{InTokens: 10, OutTokens: 2}, {InTokens: 20, OutTokens: 3, CachedTokens: 4}}) {
		t.Errorf("%d calls, %+v, each %+v", calls, u, each)
	}
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

// listed shows how many names there are, and the first 10.
func listed(names []string) string {
	s := fmt.Sprint(len(names))
	if len(names) > 0 {
		s += ": " + strings.Join(names[:min(len(names), 10)], ", ")
	}
	if len(names) > 10 {
		s += ", …"
	}
	return s
}

// planned is the ceiling and contracts cells of w's plan, "–" for a wiki no
// model planned: each rule broken, as first/afterRepair/residual.
func planned(w *wikiRun) string {
	if w == nil || w.Planning == nil {
		return "– | –"
	}
	ceiling, rules := "–", []string{}
	if c := w.Planning.Ceiling; c != nil {
		ceiling = fmt.Sprintf("%d wanted, %d planned (limit %d)", c.Wanted, c.Planned, c.Limit)
	}
	for _, r := range slices.Sorted(maps.Keys(w.Planning.Contracts)) {
		c := w.Planning.Contracts[r]
		rules = append(rules, fmt.Sprintf("%s %d/%d/%d", r, c.First, c.Repaired, c.Residual))
	}
	return ceiling + " | " + cmp.Or(strings.Join(rules, ", "), "none broken")
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
// when their first words and their last draft's came, the keywords they miss
// and whether they talk to themselves, the first wiki its time
// at each number of pages at a time, and head's wiki its tree; each
// repository's answers that did not fail, their time (a repository with none
// has no row); the report lands whole, with no file left aside.
func TestMarkdown(t *testing.T) {
	ok := result{Repo: "ok", Wiki: &wikiRun{Seconds: 10, Pages: 6, InTokens: 200, CachedTokens: 150, Planning: &wiki.Planning{Seconds: 4.25, Requests: 9,
		Contracts: map[aicontract.Rule]aicontract.Count{"section.missing": {First: 2, Repaired: 1}, "flat.json": {First: 1}}, Ceiling: &wiki.Ceiling{Limit: 64, Wanted: 80, Planned: 64}}}}
	broken := result{Repo: "broken", Wiki: &wikiRun{Failed: true, Seconds: 3, Planning: &wiki.Planning{Seconds: 1, Requests: 2}}}
	ok.Plan, ok.DeepWiki, broken.Plan = &shape.Shape{Pages: 10, Thin: 1, Homeless: []string{"svc/"}}, 28, &shape.Shape{Pages: 3}
	ok.Update.Wiki, ok.Update.PagesRewritten = &wikiRun{Seconds: 5, Pages: 6}, 1
	broken.Update.Failed, broken.Errors = true, []string{"update index: boom"}
	ok.Failed = []string{"symbols", "pages rewritten"} // a count whose query failed
	broken.Answers = []answer{{Question: "q", Error: "timeout", Trace: &trace.Trace{Gathered: 1}}}
	ok.Answers = []answer{{Question: "k", Citations: 2, Missing: []string{"Mount"}, SelfTalk: true, Language: "German", OtherLanguage: true, FirstTokenSeconds: 3, AnswerStartSeconds: 9.5, Seconds: 20,
		Rounds: 3, Trace: &trace.Trace{Gathered: 2, Shown: make([]trace.Source, 1), Evicted: make([]trace.Source, 3), Refused: make([]trace.Source, 1), Resolved: 2}, Requests: []ai.Usage{{OutTokens: 100}, {OutTokens: 50}, {OutTokens: 170}}},
		{Question: "u", Citations: 1, FirstTokenSeconds: 1, AnswerStartSeconds: 1, Seconds: 2, Rounds: 1, Trace: &trace.Trace{Gathered: 0.6, Shown: make([]trace.Source, 1), Evicted: make([]trace.Source, 1), Resolved: 1}}} // its judge failed; no reply, no usage
	ok.WikiRuns, ok.Tree = []wikiRun{{Parallel: 1, Seconds: 20}, {Parallel: 2, Seconds: 12}}, []string{"- 1 Overview", "- 2 Core", "  - 2.1 Mux"}
	ok.Pages, ok.Topics, ok.CitationSpan, ok.PageCitations = 10, &coverage{Topics: 4, Covered: 3, Missing: []string{"SSE"}, ToolingPages: 2}, citationSpan{3, 40}, 7
	uncited, uncounted := result{Repo: "uncited", Wiki: &wikiRun{}}, result{Repo: "uncounted", Wiki: &wikiRun{}, Failed: []string{"page citations"}}
	md := report{Repos: []result{ok, broken, uncited, uncounted}}.markdown()
	for _, want := range []string{"| ok | 0 | 0.0 s | 5 s | failed of 6 |", "| ok | 0 | 0 | 0 | 0 | failed | 0 | 0 | 0.0 s | 0 |", "failed after 3 s", "| broken | failed | failed |", "| broken | failed | failed | failed | failed | failed | failed | failed | 0.0 s | 1 |", "| broken | q | failed | failed | failed after 0.0 s | – | fail | no | – |", "update index: boom", "Error: timeout",
		"| ok | 1 at a time: 20 s · 2 at a time: 12 s | 6 | 0 | 200 | 150 (75%) | – |", "| broken | failed after 3 s | 0 | 0 | – | – | – |", "| ok | k | 3.0 s | 9.5 s | 20.0 s | 2 | fail: no Mount | yes | other |", "| ok | u | 1.0 s | 1.0 s | 2.0 s | 1 | pass | not judged | not judged |", "### ok (3 pages)\n\n- 1 Overview\n- 2 Core\n  - 2.1 Mux",
		"1 of 3 answers hold their keywords; 0 cite nothing; 1 think aloud; 1 answer in another language; 1 were not judged.",
		"The judge is advisory, hand labels gate; its measured error: " + judgeError + ".",
		"Evidence budget: 4 hits evicted, 1 refused; 3 citation references resolved to a path that differs from the one written by leading folders.", "| ok | 3 of 4 (75%) | 10 | 2 (20%) | SSE |",
		"Answer decode |\n|---|---|---|---|---|---|---|\n| ok | 2 | 1.0 | 1.0 s | 0.7 s | 2.0 s | 10.0 tokens/s |\n\n**broken errors:**", "| 0 | 0 | 7 | 3, at most 40 | 4.2 s, 9 requests |", "| 0 | 0 | failed | failed | 1.0 s, 2 requests |",
		"| Repository | Pages | DeepWiki | Thin | Crowded | Projects without a page | Ceiling | Contracts |\n|---|---|---|---|---|---|---|---|\n", "| ok | 10 | 28 | 1 | 0 | 1: svc/ | 80 wanted, 64 planned (limit 64) | flat.json 1/0/0, section.missing 2/1/0 |", "| broken | 3 | 0 | 0 | 0 | 0 | – | none broken |", "| 0 | 0 | 0 | – |", "| uncounted | 0 s | 0 | 0 | – | – | – | 0 | 0 | failed | failed | – |"} {
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
