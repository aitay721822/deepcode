package rival

// The capture and the measure, each run by hand: TestCapture asks DeepWiki over the network, TestCompare
// reads a stored bench run and the bare clones the retrieval eval reads.

import (
	"bytes"
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
// and keeps each answer in data/bench/deepwiki/answers.json as it comes. A question answered before is
// not asked again, and the capture stops while a question still fits the test's -timeout, so running it
// again goes on where it stopped.
func TestCapture(t *testing.T) {
	path := os.Getenv("DEEPCODE_DEEPWIKI_CAPTURE")
	if path == "" {
		t.Skip("set DEEPCODE_DEEPWIKI_CAPTURE to a bench run's JSON to ask DeepWiki its questions (it calls DeepWiki)")
	}
	rn, file := readRun(t, path), filepath.Join(stored(t), "answers.json")
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

// TestCompare compares the files each answer of a stored bench run, DEEPCODE_DEEPWIKI_MEASURE=<its
// JSON>, cites with those DeepWiki's captured answer cites, one of DeepWiki's counting only when the
// run's head holds it, as the bare clones in DEEPCODE_EVAL_GIT (<repo>.git, test/eval's) tell. It writes
// the comparison as data/bench/deepwiki/<run>.md, and fails when a question has no captured answer, as
// the comparison is then partial.
func TestCompare(t *testing.T) {
	path := os.Getenv("DEEPCODE_DEEPWIKI_MEASURE")
	if path == "" {
		t.Skip("set DEEPCODE_DEEPWIKI_MEASURE to a bench run's JSON to compare its citations with DeepWiki's")
	}
	clones := os.Getenv("DEEPCODE_EVAL_GIT")
	if clones == "" {
		t.Fatal("the measure reads the files at head from the bare clones in DEEPCODE_EVAL_GIT, as test/eval does")
	}
	rn, dir := readRun(t, path), stored(t)
	c, err := Load(filepath.Join(dir, "answers.json"))
	if err != nil {
		t.Fatal(err)
	} else if len(c.Answers) == 0 {
		t.Fatal("no DeepWiki answers are captured: run TestCapture first")
	}
	var rows []Row
	for _, r := range rn.Repos {
		out, err := exec.Command("git", "--git-dir", filepath.Join(clones, r.Repo+".git"), "ls-tree", "-r", "-z", "--name-only", r.Head).Output()
		if err != nil {
			t.Fatalf("%s: the files at %.7s: %v", r.Repo, r.Head, err)
		}
		head := map[string]bool{}
		for _, f := range bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0}) {
			head[string(f)] = true
		}
		repo := github(t, r.URL)
		for _, q := range r.Answers {
			row := Row{Repo: r.Repo, Question: q.Question, Failed: q.Error != ""}
			if ans, ok := c.Find(repo, q.Question); ok {
				o := Measure(ans.Files(), Cited(q.Answer), func(f string) bool { return head[f] })
				row.Overlap, row.Commit = &o, ans.Commit
			} else {
				t.Errorf("%s: no DeepWiki answer is captured for %q: run TestCapture to complete the capture", r.Repo, q.Question)
			}
			rows = append(rows, row)
		}
	}
	md := filepath.Join(dir, strings.TrimSuffix(filepath.Base(path), ".json")+".md")
	section := Section(rows)
	if err := os.WriteFile(md, []byte(section), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, _, _ := strings.Cut(strings.SplitN(section, "\n\n", 3)[2], "\n")
	t.Logf("wrote %s: %s", md, summary)
}
