// Package rival holds DeepWiki's answers to the bench's questions and measures, question by question, how
// far the files an answer of deepcode's cites overlap the files DeepWiki's answer cites: gate G5 of
// v1.0.0, which counts cited files rather than asking a model which answer is better. DeepWiki's answers
// are captured once (capture.go) into data/bench/deepwiki, which git ignores: they are DeepWiki's text.
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
	"strings"
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

// Capture is DeepWiki's answers, as data/bench/deepwiki/answers.json keeps them.
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

// Bar is the provisional least Ratio of a question even with DeepWiki's answer: deepcode cites at least
// half the files DeepWiki cites. Count is never below Ratio (the shared files are deepcode's too), so at
// least half as many files follows; Count is shown, not held. The user sets the final bar once the stored
// runs are measured.
const Bar = 0.5

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
	Commit         string   // the commit DeepWiki read: a file renamed since it counts as a miss
	Overlap        *Overlap // nil: no answer of DeepWiki's to the question was captured
}

// Section is the comparison as Markdown: how many of rows' questions are even, why the others are not
// measured, the median ratios of those measured, then a row a question.
func Section(rows []Row) string {
	var b strings.Builder
	var ratios, counts []float64
	var even, missing, unmeasured, failed int
	for _, r := range rows {
		if r.Failed {
			failed++
		}
		switch o := r.Overlap; {
		case o == nil:
			missing++
		case !o.Measured():
			unmeasured++
		default:
			ratios, counts = append(ratios, o.Ratio), append(counts, o.Count)
			if o.Even() {
				even++
			}
		}
	}
	fmt.Fprintf(&b, "## Against DeepWiki\n\nCited files: shared ÷ DeepWiki's (ratio) and deepcode's ÷ DeepWiki's (count), DeepWiki's counted only when head holds them. Even: a ratio of at least %.1f.\n\n", Bar)
	fmt.Fprintf(&b, "%d of %d questions even. Not even for want of a measure: %d without a captured DeepWiki answer, %d whose DeepWiki answer cites no file head holds. %d of deepcode's answers failed. Median ratio %.2f, median count %.2f over the %d measured.\n\n",
		even, len(rows), missing, unmeasured, failed, median(ratios), median(counts), len(ratios))
	b.WriteString("| Repository | Question | DeepWiki read | DeepWiki | deepcode | Shared | Ratio | Count | Even | Gone |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		o, read := r.Overlap, cmp.Or(r.Commit, "–")
		if o == nil {
			fmt.Fprintf(&b, "| %s | %s | %s | not captured | | | | | | |\n", r.Repo, r.Question, read)
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
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", r.Repo, r.Question, read, files(o.DeepWiki), mine, files(o.Shared), ratio, count, even, files(o.Gone))
	}
	return b.String()
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
