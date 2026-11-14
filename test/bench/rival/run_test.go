package rival

// The capture and the measure, each run by hand: TestCapture asks DeepWiki over the network, TestCompare
// reads stored bench runs, the captures and the bare clones the retrieval eval reads.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// run is what these tests read of a bench run's JSON (test/bench's report).
type run struct {
	Repos []struct {
		Repo    string `json:"repo"`
		URL     string `json:"url"`
		Head    string `json:"head"`
		Answers []struct {
			Question string `json:"question"`
			Answer   string `json:"answer"`
			Error    string `json:"error"`
		} `json:"answers"`
	} `json:"repos"`
}

// github is the owner/name DeepWiki knows a bench repository's url by; the bench's are all on GitHub.
func github(t *testing.T, url string) string {
	t.Helper()
	repo, ok := strings.CutPrefix(url, "https://github.com/")
	if !ok {
		t.Fatalf("%s is not on GitHub, where DeepWiki reads repositories", url)
	}
	return repo
}

func readRun(t *testing.T, path string) run {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r run
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return r
}

// stored is the folder the capture is kept in, data/bench/deepwiki, beside the bench's runs.
func stored(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "data", "bench", "deepwiki"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// perQuestion bounds one question's answer; the capture stops while the test has as long left.
const perQuestion = 5 * time.Minute

// TestCapture asks DeepWiki every question of a stored bench run, DEEPCODE_DEEPWIKI_CAPTURE=<its JSON>,
// and keeps each answer as it comes in capture DEEPCODE_DEEPWIKI_SET (1 when unset), the file
// data/bench/deepwiki/answers-<set>.json. A question that capture answered before is not asked again, and
// the capture stops while a question still fits the test's -timeout, so running it again goes on where it
// stopped.
func TestCapture(t *testing.T) {
	path := os.Getenv("DEEPCODE_DEEPWIKI_CAPTURE")
	if path == "" {
		t.Skip("set DEEPCODE_DEEPWIKI_CAPTURE to a bench run's JSON to ask DeepWiki its questions (it calls DeepWiki)")
	}
	set := cmp.Or(os.Getenv("DEEPCODE_DEEPWIKI_SET"), "1")
	name := "answers-" + set + ".json"
	if !captureName.MatchString(name) {
		t.Fatalf("DEEPCODE_DEEPWIKI_SET holds %q, which is no capture number (1, 2, ...)", set)
	}
	rn, file := readRun(t, path), filepath.Join(stored(t), name)
	c, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	s, err := Connect(ctx, Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := Asker{Session: s, HTTP: &http.Client{Timeout: time.Minute}, Searches: Searches}
	for _, r := range rn.Repos {
		repo := github(t, r.URL)
		for _, q := range r.Answers {
			if _, ok := c.Find(repo, q.Question); ok {
				continue
			}
			if end, ok := t.Deadline(); ok && time.Until(end) < perQuestion+time.Minute {
				t.Logf("stopped before the timeout with %d answers kept: run again to go on", len(c.Answers))
				return
			}
			qctx, cancel := context.WithTimeout(ctx, perQuestion)
			ans, err := a.Ask(qctx, repo, q.Question)
			cancel()
			if err != nil {
				t.Errorf("%s: %s: %v", repo, q.Question, err)
				continue
			}
			c.Answers = append(c.Answers, ans)
			if err := c.Save(file); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %d files cited: %s", repo, len(ans.Files()), q.Question)
		}
	}
}

// TestCompare compares the code files each answer of the stored bench runs DEEPCODE_DEEPWIKI_MEASURE
// names (their JSON, separated as a PATH list is) cites with those every capture's answer cites
// (data/bench/deepwiki/answers-<n>.json), one of DeepWiki's counting only when the run's head holds it, by
// its name there when renamed since the commit DeepWiki read, as the bare clones in DEEPCODE_EVAL_GIT
// (<repo>.git, test/eval's) tell. It writes the comparison, gate G5's verdict among it, as
// data/bench/deepwiki/<the runs' names, joined by +>.md, and fails when a question has no captured answer,
// as the comparison is then partial; G5 not met is reported, not failed.
func TestCompare(t *testing.T) {
	runs := filepath.SplitList(os.Getenv("DEEPCODE_DEEPWIKI_MEASURE"))
	if len(runs) == 0 {
		t.Skip("set DEEPCODE_DEEPWIKI_MEASURE to bench runs' JSON to compare their citations with DeepWiki's")
	}
	clones := os.Getenv("DEEPCODE_EVAL_GIT")
	if clones == "" {
		t.Fatal("the measure reads the files at head from the bare clones in DEEPCODE_EVAL_GIT, as test/eval does")
	}
	dir := stored(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	files := Captures(names)
	if len(files) == 0 {
		t.Fatal("no DeepWiki answers are captured: run TestCapture first")
	}
	captures := make([]Capture, len(files))
	for i, f := range files {
		if captures[i], err = Load(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	heads := map[string]map[string]bool{} // the files at each repository's head, by clone and commit
	renames := map[string]map[string]string{}
	var pairs []Pair
	stems, err := Names(runs)
	if err != nil {
		t.Fatal(err)
	}
	for k, path := range runs {
		rn := readRun(t, path)
		for i, c := range captures {
			p := Pair{Run: stems[k], Capture: files[i]}
			for _, r := range rn.Repos {
				head := atHead(t, heads, filepath.Join(clones, r.Repo+".git"), r.Head)
				repo := github(t, r.URL)
				for _, q := range r.Answers {
					row := Row{Repo: r.Repo, Question: q.Question, Failed: q.Error != ""}
					if ans, ok := c.Find(repo, q.Question); ok {
						dw, moved := Rename(ans.Files(), renamed(t, renames, filepath.Join(clones, r.Repo+".git"), ans.Commit, r.Head))
						o := Measure(Code(dw), Code(Cited(q.Answer)), func(f string) bool { return head[f] })
						row.Overlap, row.Commit, row.Renamed = &o, ans.Commit, moved
					} else {
						t.Errorf("%s has no DeepWiki answer for %s: %q: run TestCapture to complete it", p.Capture, r.Repo, q.Question)
					}
					p.Rows = append(p.Rows, row)
				}
			}
			pairs = append(pairs, p)
		}
	}
	md := filepath.Join(dir, strings.Join(stems, "+")+".md")
	if err := os.WriteFile(md, []byte(Report(pairs)), 0o600); err != nil {
		t.Fatal(err)
	}
	v := G5(pairs)
	t.Logf("wrote %s: a mean of %.2f even questions of %.2f asked, against G5's bar of %.2f: met %v", md, v.Mean, v.Asked, v.Bar, v.Met)
}

// renamed is the files renamed (Renames) from the commit DeepWiki read to head in the bare clone at git,
// read once into cache. A commit the clone lacks renames nothing, as the log says: its files renamed since
// count as gone.
func renamed(t *testing.T, cache map[string]map[string]string, git, from, head string) map[string]string {
	t.Helper()
	key := git + "@" + from + ".." + head
	if r, ok := cache[key]; ok {
		return r
	}
	out, err := exec.Command("git", "--git-dir", git, "diff", "-M", "--name-status", "-z", from, head).Output()
	if err != nil {
		t.Logf("%s: the renames from %s to %.7s are unread, so its files renamed since count as gone: %v", git, from, head, err)
		out = nil
	}
	cache[key] = Renames(out)
	return cache[key]
}

// atHead is the set of files the bare clone at git holds at commit head, read once into heads.
func atHead(t *testing.T, heads map[string]map[string]bool, git, head string) map[string]bool {
	t.Helper()
	key := git + "@" + head
	if files, ok := heads[key]; ok {
		return files
	}
	out, err := exec.Command("git", "--git-dir", git, "ls-tree", "-r", "-z", "--name-only", head).Output()
	if err != nil {
		t.Fatalf("%s: the files at %.7s: %v", git, head, err)
	}
	files := map[string]bool{}
	for _, f := range bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0}) {
		files[string(f)] = true
	}
	heads[key] = files
	return files
}
