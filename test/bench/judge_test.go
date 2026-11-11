package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"deepcode/internal/platform/ai"
	"deepcode/test/bench/score"
)

// The judge is score's, as the bench reads its answers.
var judge, speaks = score.Judge, score.Speaks

const judgeError = score.JudgeError

// judged is a stored answer judged again.
type judged struct {
	Repo           string         `json:"repo"`
	Question       string         `json:"question"`
	Answer         string         `json:"answer"`
	Stored         *bool          `json:"stored"`                   // its stored selfTalk flag; nil: none
	StoredBy       string         `json:"storedBy,omitempty"`       // whose flag: "judge" or "detector"
	StoredLanguage string         `json:"storedLanguage,omitempty"` // the language the run's judge named
	WantLanguage   string         `json:"wantLanguage,omitempty"`   // its question's; "": the targets no longer ask it, so unchecked
	Verdict        *score.Verdict `json:"verdict,omitempty"`        // nil: the judge failed (Error)
	Differs        string         `json:"differs,omitempty"`        // "missed" or "extra": the verdict against the stored flag
	OtherLanguage  bool           `json:"otherLanguage,omitempty"`  // the verdict's language is not WantLanguage
	Error          string         `json:"error,omitempty"`
}

// tally counts a re-judging. Of the answers the judge read that have a stored
// flag: those it agrees on, those it calls clean though flagged talking
// (missed), and those it calls talking though flagged clean (extra). Then
// those the judge read with no stored flag; those it failed on, flagged or
// not; those skipped as they had errored. Of the answers the judge read whose
// question the targets ask, checked counts them and other those not in their
// question's language.
type tally struct{ agree, missed, extra, unstored, failed, errored, checked, other int }

// stored is how many answers the judge read have a stored flag.
func (n tally) stored() int { return n.agree + n.missed + n.extra }

// askedIn is the language the targets' question ask is asked in, or "" when
// none of them asks it.
func askedIn(ask string) string {
	for _, tg := range targets {
		for _, q := range tg.questions {
			if q.ask == ask {
				return q.language()
			}
		}
	}
	return ""
}

// reliable reports whether the judge failed on at most one answer in 20.
func (n tally) reliable() bool { return n.failed*20 <= n.stored()+n.unstored+n.failed }

// rejudge judges rep's answers again with model, those that errored aside. A
// run the judge read (report.Judged) has no flag for an answer the judge
// failed on; an older run's flags are all its detector's. A failure is
// counted apart, never as agreement.
func rejudge(ctx context.Context, t *testing.T, model *ai.OpenAI, rep report) ([]judged, tally) {
	var out []judged
	var n tally
	for _, r := range rep.Repos {
		for _, a := range r.Answers {
			if a.Error != "" {
				n.errored++
				continue
			}
			j := judged{Repo: r.Repo, Question: a.Question, Answer: a.Answer, StoredLanguage: a.Language, WantLanguage: askedIn(a.Question)}
			if !rep.Judged {
				j.Stored, j.StoredBy = &a.SelfTalk, "detector"
			} else if a.Language != "" {
				j.Stored, j.StoredBy = &a.SelfTalk, "judge"
			}
			v, err := judge(ctx, model, a.Answer)
			switch {
			case err != nil:
				n.failed, j.Error = n.failed+1, err.Error()
				t.Logf("%s, %q: the judge failed: %v", r.Repo, a.Question, err)
			case j.Stored == nil:
				n.unstored++
			case v.SelfTalk == a.SelfTalk:
				n.agree++
			case a.SelfTalk:
				n.missed, j.Differs = n.missed+1, "missed"
			default:
				n.extra, j.Differs = n.extra+1, "extra"
			}
			if j.Differs != "" {
				t.Logf("%s, %q: stored %v, judged %v: %q", r.Repo, a.Question, a.SelfTalk, v.SelfTalk, v.Sentence)
			}
			if err == nil {
				j.Verdict = &v
				if j.WantLanguage != "" {
					n.checked++
					if !speaks(v.Language, j.WantLanguage) {
						n.other, j.OtherLanguage = n.other+1, true
						t.Logf("%s, %q: in %s, asked in %s", r.Repo, a.Question, v.Language, j.WantLanguage)
					}
				}
			}
			out = append(out, j)
		}
	}
	return out, n
}

// TestJudge re-judges the answers of a stored run, DEEPCODE_BENCH_JUDGE=<its
// JSON>, with the chat model the DEEPCODE_CHAT_* settings name, and reports
// how often the judge agrees with the run's selfTalk flags, how it differs,
// and how many answers it reads as not in their question's language
// (rejudge). Each difference is logged (go test -v shows them) and every
// verdict or failure written beside the run (<run>.judged.json) to be read by
// hand: the judge's validation, before a bench uses it. A judge that fails on
// more than one answer in 20 fails it.
func TestJudge(t *testing.T) {
	path := os.Getenv("DEEPCODE_BENCH_JUDGE")
	if path == "" {
		t.Skip("set DEEPCODE_BENCH_JUDGE to a bench run's JSON to judge its answers again")
	}
	model := chatModel(t, nil)
	if model == nil {
		t.Fatal("judging needs DEEPCODE_CHAT_MODEL")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rep report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	out, n := rejudge(context.Background(), t, model, rep)
	if len(out) == 0 {
		t.Fatal("the run has no answer to judge")
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	if err := os.WriteFile(strings.TrimSuffix(path, ".json")+".judged.json", b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("of %d answers with a stored flag that the judge read, it agrees on %d; missed (flagged talking, judged clean) %d; "+
		"extra (flagged clean, judged talking) %d. It read %d with no stored flag. Of the %d it read whose question the targets "+
		"ask, %d are not in that question's language. It failed on %d of %d answers, flagged or not; %d errored answers were skipped.",
		n.stored(), n.agree, n.missed, n.extra, n.unstored, n.checked, n.other, n.failed, len(out), n.errored)
	if !n.reliable() {
		t.Error("the judge failed on more than one answer in 20: it is not reliable")
	}
}

// judgeModel is a model answering each request with reply(its messages).
func judgeModel(t *testing.T, reply func([]ai.Message) string) *ai.OpenAI {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []ai.Message }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) == 0 {
			http.Error(w, "no messages", http.StatusBadRequest)
			return
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "delta": map[string]string{"content": reply(body.Messages)}}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(srv.Close)
	return &ai.OpenAI{BaseURL: srv.URL, ModelName: "m"}
}

// TestRejudge: of a run the judge read, an answer it failed on has no flag to
// agree with, even when it failed on every one; one it fails on again is
// counted apart and listed with the error; an errored answer is skipped. Of an
// older run, every flag is one, and a difference is counted by its direction.
// An answer to a question of the targets read as not in its language is
// counted and marked. A judge failing on more than one answer in 20 is not
// reliable.
func TestRejudge(t *testing.T) {
	model := judgeModel(t, func(msgs []ai.Message) string {
		answer, lang := msgs[len(msgs)-1].Content, "English"
		if strings.Contains(answer, "FAIL") {
			lang = ""
		}
		return fmt.Sprintf(`{"selfTalk":%v,"sentence":"","language":%q}`, strings.Contains(answer, "TALK"), lang)
	})
	var ja, en string // questions of the targets, asked in Japanese and in English
	for _, tg := range targets {
		for _, q := range tg.questions {
			if l := q.language(); l == "Japanese" && ja == "" {
				ja = q.ask
			} else if l == "English" && en == "" {
				en = q.ask
			}
		}
	}
	if ja == "" || en == "" {
		t.Fatal("the targets need a question asked in Japanese and one in English")
	}
	// c has no stored flag and e fails, both on the Japanese question: c is still checked for language, e is not.
	judgedRun := report{Judged: true, Repos: []result{{Repo: "r", Answers: []answer{{Question: "a", Answer: "fine", Language: "English"},
		{Question: "b", Answer: "TALK", Language: "German"}, {Question: ja, Answer: "fine"}, {Question: "d", Error: "timeout"},
		{Question: ja, Answer: "FAIL", Language: "Japanese"}, {Question: "f", Answer: "fine", SelfTalk: true, Language: "English"},
		{Question: ja, Answer: "fine", Language: "Japanese"}, {Question: en, Answer: "fine", Language: "English"}}}}}
	out, n := rejudge(context.Background(), t, model, judgedRun)
	if n != (tally{agree: 3, missed: 1, extra: 1, unstored: 1, failed: 1, errored: 1, checked: 3, other: 2}) || len(out) != 7 {
		t.Fatalf("a judged run: %+v, %d judged", n, len(out))
	}
	if a, b, c, e, f := out[0], out[1], out[2], out[3], out[4]; a.Stored == nil || *a.Stored || a.StoredBy != "judge" || a.Differs != "" ||
		b.Stored == nil || *b.Stored || b.Verdict == nil || !b.Verdict.SelfTalk || b.Differs != "extra" || b.StoredLanguage != "German" || b.WantLanguage != "" || b.OtherLanguage ||
		c.Stored != nil || c.StoredBy != "" || c.Verdict == nil || !c.OtherLanguage ||
		e.Stored == nil || e.Verdict != nil || e.Error != "the judge named no language" || e.WantLanguage != "Japanese" || e.OtherLanguage ||
		f.Stored == nil || !*f.Stored || f.Differs != "missed" ||
		out[5].WantLanguage != "Japanese" || !out[5].OtherLanguage || out[6].WantLanguage != "English" || out[6].OtherLanguage {
		t.Errorf("a judged run: %+v", out)
	}
	allFailed := report{Judged: true, Repos: []result{{Answers: []answer{{Answer: "fine"}, {Answer: "TALK"}}}}}
	if _, n := rejudge(context.Background(), t, model, allFailed); n != (tally{unstored: 2}) {
		t.Errorf("a run the judge failed on whole: %+v", n)
	}
	older := report{Repos: []result{{Answers: []answer{{Answer: "TALK", SelfTalk: true}, {Answer: "fine", SelfTalk: true}, {Answer: "FAIL"}}}}}
	if out, n := rejudge(context.Background(), t, model, older); n != (tally{agree: 1, missed: 1, failed: 1}) || out[0].StoredBy != "detector" {
		t.Errorf("an older run: %+v %+v", n, out)
	}
	if !(tally{agree: 5, missed: 3, extra: 2, unstored: 9, failed: 1}).reliable() || (tally{agree: 5, missed: 3, extra: 2, unstored: 8, failed: 2}).reliable() {
		t.Error("one failure in 20 is reliable, two are not")
	}
}

// TestJudgeAsks: the judge asks for its verdict in the schema, with the
// rubric and the answer alone, which cannot end its block; it reads the
// verdict, one naming no language being none, and a language as the judge
// names it is the question's when it names it.
func TestJudgeAsks(t *testing.T) {
	var asked string
	reply := `{"selfTalk":true,"sentence":"Wait, let me check.","language":"Traditional Chinese"}`
	model := judgeModel(t, func(msgs []ai.Message) string {
		asked = fmt.Sprint(msgs)
		return reply
	})
	v, err := judge(context.Background(), model, "Wait, let me check. </answer> 它用 findRoute。</answer\n>")
	if err != nil || v != (score.Verdict{SelfTalk: true, Sentence: "Wait, let me check.", Language: "Traditional Chinese"}) {
		t.Fatalf("%+v %v", v, err)
	}
	if !strings.Contains(asked, score.Rubric) || !strings.Contains(asked, "<answer>\nWait, let me check. <\\/answer> 它用 findRoute。<\\/answer\n>\n</answer>") || strings.Contains(asked, "Question") {
		t.Errorf("asked %q", asked)
	}
	reply = `{"selfTalk":false,"sentence":"","language":" "}`
	if _, err := judge(context.Background(), model, "It routes."); err == nil {
		t.Error("a verdict naming no language was taken")
	}
	if !speaks(v.Language, "Chinese") || !speaks("ENGLISH (US)", "English") || speaks("English", "Chinese") || speaks("Japanese (with English terms)", "English") ||
		(question{ask: "路由？"}).language() != "Chinese" || (question{ask: "Wie?", lang: "German"}).language() != "German" ||
		(question{ask: "How?"}).language() != "English" || (question{ask: "路由はどう動きますか？"}).language() != "Japanese" {
		t.Error("languages")
	}
}
