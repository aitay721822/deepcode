package aside

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/repo/index/analyzer"
)

// keys are n guide keys under folder key, key + "000.md" and on.
func keys(key string, n int) []string {
	var out []string
	for i := range n {
		out = append(out, fmt.Sprintf("%s%03d.md", key, i))
	}
	return out
}

// TestSpread: each group apart is asked in even batches of at most most,
// path order, after the pages so far, each for one page and its members'
// weight's share of the rest of the group's Cap (one batch the whole Cap),
// no more than its units, so no more than Cap in all, a Cap of three over
// three batches one each; the pages each gives are added in turn; a batch's
// request failing fails the spread.
func TestSpread(t *testing.T) {
	files := append(append(keys("a/", 10), keys("c/", 250)...), keys("e/", 101)...)
	lines := map[string]int{}
	for _, f := range keys("c/", 84) { // c/'s first batch weighs three times the others
		lines[f] = 3
	}
	for _, f := range keys("e/", 51) {
		lines[f] = 100
	}
	units := place.AsideUnits(analyzer.Docs, files, lines, nil)
	apart := []place.Unit{{Key: "a/", Raw: keys("a/", 10), Cap: 4}, {Key: "c/", Raw: keys("c/", 250), Cap: 30}, {Key: "e/", Raw: keys("e/", 101), Cap: 101}}
	var asked []string
	got, err := Spread([]outline.Page{{Title: "P0"}}, apart, units, 100, func(pages []outline.Page, g place.Unit, batch []place.Unit, k int) ([]outline.Page, error) {
		asked = append(asked, fmt.Sprintf("%s %d %s k%d after %d", g.Key, len(batch), batch[0].Key, k, len(pages)))
		return []outline.Page{{Title: batch[0].Key}}, nil
	})
	want := []string{"a/ 10 a/000.md k4 after 1", "c/ 84 c/000.md k17 after 2", "c/ 83 c/084.md k6 after 3", "c/ 83 c/167.md k6 after 4",
		"e/ 51 e/000.md k51 after 5", "e/ 50 e/051.md k1 after 6"}
	if err != nil || !reflect.DeepEqual(asked, want) || len(got) != 7 || got[0].Title != "P0" || got[6].Title != "e/051.md" {
		t.Errorf("asked %v, %d pages (%v)", asked, len(got), err)
	}
	// Even weights: 250 units of a Cap of 30 ask 10, 9 and 9; of a Cap of 3, one each.
	for cap, want := range map[int]string{30: "10 9 9", 3: "1 1 1"} {
		asked = nil
		Spread(nil, []place.Unit{{Key: "c/", Raw: keys("c/", 250), Cap: cap}}, place.AsideUnits(analyzer.Docs, keys("c/", 250), nil, nil), 100,
			func(_ []outline.Page, _ place.Unit, batch []place.Unit, k int) ([]outline.Page, error) {
				asked = append(asked, fmt.Sprint(k))
				return nil, nil
			})
		if strings.Join(asked, " ") != want {
			t.Errorf("even weights, a Cap of %d, asked %v", cap, asked)
		}
	}
	if _, err := Spread(nil, apart, units, 100, func([]outline.Page, place.Unit, []place.Unit, int) ([]outline.Page, error) {
		return nil, errors.New("down")
	}); err == nil {
		t.Error("a failed batch passed")
	}
	// Batches are the fewest: a group of 100 is one, of 200 two.
	if one, two := batches(make([]place.Unit, 100), 100), batches(make([]place.Unit, 200), 100); len(one) != 1 || len(two) != 2 || len(two[1]) != 100 {
		t.Errorf("batches of 100 and 200: %d, %d", len(one), len(two))
	}
}

// TestMessage: a level-2 message holds the aside's lines for the batch, the
// section's pages so far (none, no line), and what the batch is of its group;
// it states no number of pages, as no message does (contract's G7f).
func TestMessage(t *testing.T) {
	g := place.Unit{Key: "docs/", Raw: keys("docs/", 12), Cap: 7}
	batch := place.AsideUnits(analyzer.Docs, []string{"docs/q.md"}, nil, nil)
	m := Message("", []string{"S"}, "Docs", analyzer.Docs, []outline.Page{{Title: "Install"}, {Title: "Usage"}}, g, batch, map[string][]string{"docs/q.md": {"Questions"}}, nil)
	for _, want := range []string{"The section's pages so far: Install | Usage\n", `- "docs/q.md" (1 files, 0 lines): Questions`,
		"These are 1 of the 12 units of \"docs/\", planned apart in pages of their own: plan those pages, none taking another page's subject.\n"} {
		if !strings.Contains(m, want) {
			t.Errorf("message lacks %q:\n%s", want, m)
		}
	}
	count := regexp.MustCompile(`(?i)\b(\d+|(up to|at most|at least|exactly) \w+)\s+(pages?|sections?)\b`)
	if c := count.FindString(m); c != "" || strings.Contains(m, "7") {
		t.Errorf("a count of pages, %q:\n%s", c, m)
	}
	if m := Message("", nil, "Docs", analyzer.Docs, nil, g, batch, nil, nil); strings.Contains(m, "so far") {
		t.Errorf("no pages yet:\n%s", m)
	}
}
