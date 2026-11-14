// Package rival holds DeepWiki's answers to the bench's questions and measures, question by question, how
// far the code files an answer of deepcode's cites overlap the code files DeepWiki's answer cites: gate G5
// of v1.0.0, which counts cited files rather than asking a model which answer is better. DeepWiki's
// answers are captured (capture.go) into data/bench/deepwiki, which git ignores as they are DeepWiki's
// text; two captures stay fixed as the gate's reference until one is taken again on purpose.
package rival

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"deepcode/internal/repo/index/analyzer"
)

// Ref is a range of a file that DeepWiki's answer cites, its lines counted from 1.
type Ref struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Answer is DeepWiki's answer to one question: its text, the search DeepWiki keeps it as
// (deepwiki.com/search/<Slug>), the commit DeepWiki read, the ranges the answer cites and the files
// DeepWiki read for it.
type Answer struct {
	Repo     string   `json:"repo"` // owner/name
	Question string   `json:"question"`
	Captured string   `json:"captured"` // when, RFC 3339
	Text     string   `json:"text"`
	Slug     string   `json:"slug"`
	Commit   string   `json:"commit"`
	Refs     []Ref    `json:"refs"`
	Read     []string `json:"read"`
}

// Capture is DeepWiki's answers, as data/bench/deepwiki/answers-<n>.json keeps capture n.
type Capture struct {
	Answers []Answer `json:"answers"`
}

// Load reads the capture at path; none there is an empty one.
func Load(path string) (Capture, error) {
	var c Capture
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	} else if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

// Save writes c to path, making its folder.
func (c Capture) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// captureName is a capture's file name, answers-<n>.json for capture n, numbered from 1.
var captureName = regexp.MustCompile(`^answers-([1-9][0-9]*)\.json$`)

// Captures is those of names that name a capture, in the order of their numbers.
func Captures(names []string) []string {
	out := slices.DeleteFunc(slices.Clone(names), func(n string) bool { return !captureName.MatchString(n) })
	number := func(n string) int { i, _ := strconv.Atoi(captureName.FindStringSubmatch(n)[1]); return i }
	slices.SortFunc(out, func(a, b string) int { return cmp.Compare(number(a), number(b)) })
	return out
}

// Names is each run's name, its file's name less .json. Two runs of one name are an error, as the report
// tells runs apart, and is named, by their names.
func Names(runs []string) ([]string, error) {
	var names []string
	for _, r := range runs {
		n := strings.TrimSuffix(filepath.Base(r), ".json")
		if slices.Contains(names, n) {
			return nil, fmt.Errorf("two runs are named %s: the report tells runs apart by their files' names", n)
		}
		names = append(names, n)
	}
	return names, nil
}

// Find is DeepWiki's answer to question about repo (owner/name).
func (c Capture) Find(repo, question string) (Answer, bool) {
	i := slices.IndexFunc(c.Answers, func(a Answer) bool { return a.Repo == repo && a.Question == question })
	if i < 0 {
		return Answer{}, false
	}
	return c.Answers[i], true
}

// Files is the distinct files a cites, in the order first cited.
func (a Answer) Files() []string {
	var out []string
	for _, r := range a.Refs {
		if !slices.Contains(out, r.Path) {
			out = append(out, r.Path)
		}
	}
	return out
}

// blob is a link into a commit's file, as gitserver.SourceURL writes one for GitHub and GitLab (/-/blob/).
var blob = regexp.MustCompile(`/blob/[0-9a-f]{7,40}/([^#?\s)]+)`)

// Cited is the distinct files an answer of deepcode's cites, in the order first cited: the paths of its
// links into a commit's files, unescaped.
func Cited(answer string) []string {
	var out []string
	for _, m := range blob.FindAllStringSubmatch(answer, -1) {
		p, err := url.PathUnescape(m[1])
		if err != nil {
			p = m[1]
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// Bar is the least Ratio of a question even with DeepWiki's answer: deepcode cites at least half the code
// files DeepWiki cites. Count is never below Ratio (the shared files are deepcode's too), so at least half
// as many follows; Count is shown, not held.
const Bar = 0.5

// Need is gate G5: the least mean number of even questions, over every run and capture compared, of the
// bench's Questions. It is DeepWiki's agreement with itself less 2, as the user set it: a run made of each
// capture's citations, measured by TestCompare against the other capture, renames followed, is even on 40
// and 40 of the 43 questions (2026-11-14), so deepcode is to cite about what DeepWiki cites as nearly as
// DeepWiki repeats itself.
const Need = 38

// Questions is the number of questions the bench asks over its repositories, those Need counts of.
const Questions = 43

// Code is those of paths that are code: tests, examples, docs, prose and hidden folders aside, as deepcode
// itself ranks them down (analyzer.Secondary). DeepWiki cites READMEs, guides and tests far more often
// than deepcode does, by habit rather than for what the code does.
func Code(paths []string) []string {
	return slices.DeleteFunc(slices.Clone(paths), analyzer.Secondary)
}

// Overlap compares the files two answers to one question cite.
type Overlap struct {
	DeepWiki []string // the files DeepWiki's answer cites that head holds
	Gone     []string // those head no longer holds, left out of the measure
	Deepcode []string
	Shared   []string
	Ratio    float64 // len(Shared) / len(DeepWiki)
	Count    float64 // len(Deepcode) / len(DeepWiki)
}

// Measure compares the files DeepWiki's answer cites, deepwiki, with those deepcode's cites, deepcode;
// a file at does not hold at head counts as gone. With none of DeepWiki's left, nothing is measured.
func Measure(deepwiki, deepcode []string, at func(string) bool) Overlap {
	o := Overlap{Deepcode: deepcode}
	for _, f := range deepwiki {
		if at(f) {
			o.DeepWiki = append(o.DeepWiki, f)
		} else {
			o.Gone = append(o.Gone, f)
		}
	}
	for _, f := range o.DeepWiki {
		if slices.Contains(deepcode, f) {
			o.Shared = append(o.Shared, f)
		}
	}
	if n := float64(len(o.DeepWiki)); n > 0 {
		o.Ratio, o.Count = float64(len(o.Shared))/n, float64(len(deepcode))/n
	}
	return o
}

// Measured reports whether DeepWiki's answer cites a file head holds: without one nothing is compared,
// so the question is not even.
func (o Overlap) Measured() bool { return len(o.DeepWiki) > 0 }

// Even reports whether the question is even with DeepWiki's answer: o is measured and its Ratio
// reaches Bar.
func (o Overlap) Even() bool { return o.Measured() && o.Ratio >= Bar }

// Row is one question of a run against DeepWiki's answer.
type Row struct {
	Repo, Question string
	Failed         bool     // deepcode's answer failed (the run's error), so it cites nothing
	Commit         string   // the commit DeepWiki read
	Renamed        []string // DeepWiki's files renamed since Commit, "old → new", measured by their name at head
	Overlap        *Overlap // nil: no answer of DeepWiki's to the question was captured
}

// Renames reads `git diff -M --name-status -z` from the commit DeepWiki read to head: each file renamed
// since, by its old path. A copy (C) leaves its source in place, so only renames (R) count.
func Renames(z []byte) map[string]string {
	f := strings.Split(strings.TrimSuffix(string(z), "\x00"), "\x00")
	out := map[string]string{}
	for i := 0; i < len(f) && f[i] != ""; {
		switch f[i][0] {
		case 'R':
			if i+2 < len(f) {
				out[f[i+1]] = f[i+2]
			}
			i += 3
		case 'C':
			i += 3
		default:
			i += 2
		}
	}
	return out
}

// Rename is paths by their names at head, as renames gives them, and the renames taken, "old → new".
func Rename(paths []string, renames map[string]string) (out, moved []string) {
	for _, p := range paths {
		if to, ok := renames[p]; ok {
			moved = append(moved, p+" → "+to)
			p = to
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out, moved
}

// Tally is how one run's answers fare against one capture: of its questions, those even, those with no
// captured answer, those whose DeepWiki answer cites no code file head holds, and deepcode's answers that
// failed; Ratio and Count are the medians of the Measured questions.
type Tally struct {
	Questions, Even, Missing, Unmeasured, Failed, Measured int
	Ratio, Count                                           float64
}

// Score tallies rows.
func Score(rows []Row) Tally {
	t := Tally{Questions: len(rows)}
	var ratios, counts []float64
	for _, r := range rows {
		if r.Failed {
			t.Failed++
		}
		switch o := r.Overlap; {
		case o == nil:
			t.Missing++
		case !o.Measured():
			t.Unmeasured++
		default:
			ratios, counts = append(ratios, o.Ratio), append(counts, o.Count)
			if o.Even() {
				t.Even++
			}
		}
	}
	t.Measured, t.Ratio, t.Count = len(ratios), median(ratios), median(counts)
	return t
}

// Pair is one run's answers against one capture of DeepWiki's, each named as its file is.
type Pair struct {
	Run, Capture string
	Rows         []Row
}

// Verdict is gate G5 over the pairs compared: the mean numbers of even questions and of questions asked
// per pair, the Bar Mean must reach (Need of every Questions, scaled to the questions asked, so a run
// holding some of the bench's repositories is held to the same rate) and whether it does.
type Verdict struct {
	Mean, Asked, Bar float64
	Met              bool
}

// G5 is the verdict over pairs; none, or none holding a question, is not met.
func G5(pairs []Pair) Verdict {
	even, all := 0, 0
	for _, p := range pairs {
		t := Score(p.Rows)
		even, all = even+t.Even, all+t.Questions
	}
	if all == 0 {
		return Verdict{}
	}
	n := float64(len(pairs))
	return Verdict{Mean: float64(even) / n, Asked: float64(all) / n, Bar: float64(Need*all) / Questions / n, Met: even*Questions >= Need*all}
}

// Report is the comparison as Markdown: each pair's tally, the verdict G5, then each pair's questions, a
// row each.
func Report(pairs []Pair) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Against DeepWiki\n\nCode files only (analyzer.Secondary's tests, examples, docs and prose left out). A question is even when deepcode cites at least %.0f%% of the code files DeepWiki cites that head holds (ratio); count is deepcode's code files ÷ DeepWiki's. A question with no captured answer, or whose DeepWiki answer cites no code file head holds, is not even.\n\n", Bar*100)
	b.WriteString("| Run | DeepWiki capture | Even | Measured | Not captured | Cites no code file | deepcode failed | Median ratio | Median count |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, p := range pairs {
		t := Score(p.Rows)
		fmt.Fprintf(&b, "| %s | %s | %d of %d | %d | %d | %d | %d | %.2f | %.2f |\n", p.Run, p.Capture, t.Even, t.Questions, t.Measured, t.Missing, t.Unmeasured, t.Failed, t.Ratio, t.Count)
	}
	v := G5(pairs)
	fmt.Fprintf(&b, "\nG5 (run × capture pairs compared: %d): a mean of %.2f even questions of %.2f asked, against a bar of %.2f (%d of the bench's %d, DeepWiki's agreement with itself less 2, scaled to the questions asked): %s.\n",
		len(pairs), v.Mean, v.Asked, v.Bar, Need, Questions, map[bool]string{true: "met", false: "not met"}[v.Met])
	if v.Asked > 0 && v.Asked < Questions {
		b.WriteString("The runs hold a sample of the bench's questions: the verdict speaks for the repositories they hold, not for the bench.\n")
	}
	for _, p := range pairs {
		fmt.Fprintf(&b, "\n## %s against %s\n\n", p.Run, p.Capture)
		table(&b, p.Rows)
	}
	return b.String()
}

// table writes rows as a Markdown table, a row a question.
func table(b *strings.Builder, rows []Row) {
	b.WriteString("| Repository | Question | DeepWiki read | DeepWiki | deepcode | Shared | Ratio | Count | Even | Gone or renamed |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		o, read := r.Overlap, cmp.Or(r.Commit, "–")
		if o == nil {
			fmt.Fprintf(b, "| %s | %s | %s | not captured | | | | | | |\n", r.Repo, r.Question, read)
			continue
		}
		mine := files(o.Deepcode)
		if r.Failed {
			mine = "failed"
		}
		ratio, count, even := "–", "–", "unmeasured"
		if o.Measured() {
			ratio, count, even = fmt.Sprintf("%.2f", o.Ratio), fmt.Sprintf("%.2f", o.Count), map[bool]string{true: "yes", false: "no"}[o.Even()]
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", r.Repo, r.Question, read, files(o.DeepWiki), mine, files(o.Shared), ratio, count, even, files(slices.Concat(o.Gone, r.Renamed)))
	}
}

// files lists paths for a table cell.
func files(paths []string) string {
	if len(paths) == 0 {
		return "–"
	}
	return strings.Join(paths, ", ")
}

// median is the middle of xs, 0 for none.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s, n := slices.Sorted(slices.Values(xs)), len(xs)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
