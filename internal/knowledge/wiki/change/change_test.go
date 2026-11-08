package change

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func TestIs(t *testing.T) {
	for p, want := range map[string]bool{"package.json": true, "web/package.json": true, "go.mod": true, "Cargo.toml": true,
		"src/Shop.Web/Shop.Web.csproj": true, "eShop.slnx": true, "build.gradle.kts": true, "pom.xml": true,
		"main.go": false, "package.json.md": false, "src/go.mod.go": false, "README.md": false} {
		if Is(p) != want {
			t.Errorf("Is(%q) = %v", p, !want)
		}
	}
}

// TestDiff: where each old line went, 0 for one replaced or removed.
func TestDiff(t *testing.T) {
	for _, c := range []struct {
		before, after string
		to            []int
	}{
		{"{\n  \"a\": \"1\",\n  \"b\": \"2\"\n}\n", "{\n  \"a\": \"1\",\n  \"b\": \"3\"\n}\n", []int{1, 2, 0, 4, 5}}, // one version bumped
		{"a\nc", "a\nb\nc", []int{1, 3}},                         // an insertion
		{"a\nb\nc", "a\nc", []int{1, 0, 2}},                      // a deletion
		{"a\nb\nc\nd", "x\nb\nc\ny", []int{0, 2, 3, 0}},          // two edits
		{"a", "a\nb", []int{1}},                                  // appended
		{"a\nb", "b", []int{0, 1}},                               // the first line removed
		{"a\nb\nc\nd\ne", "c\nd\ne\na\nb", []int{0, 0, 1, 2, 3}}, // a block moved: the longer run stays
		{"a\nb", "a\nb", []int{1, 2}},                            // a change of mode only
	} {
		if got := Diff(c.before, c.after).to; !slices.Equal(got, c.to) {
			t.Errorf("Diff(%q, %q) = %v, want %v", c.before, c.after, got, c.to)
		}
	}
	// Past maxEdits the lines between those kept at either end count as replaced.
	var a, b []string
	for i := range 2 * maxEdits {
		a, b = append(a, fmt.Sprint("a", i)), append(b, fmt.Sprint("b", i))
	}
	far := Diff("head\n"+strings.Join(a, "\n")+"\ntail", "head\n"+strings.Join(b, "\n")+"\ntail")
	if far.to[0] != 1 || far.to[len(far.to)-1] != len(far.to) || slices.ContainsFunc(far.to[1:len(far.to)-1], func(n int) bool { return n != 0 }) {
		t.Errorf("a change past maxEdits kept %v", far.to)
	}
}

// TestKeeps: a citation survives a change only in one piece, and then moves
// with its lines; one the change broke spans what replaced it.
func TestKeeps(t *testing.T) {
	e := Diff("a\nb\nc\nd\ne", "a\nx\nb\nc\nd\ne")
	for _, c := range []struct {
		first, last, from, to int
		keeps                 bool
	}{{1, 1, 1, 1, true}, {2, 4, 3, 5, true}, {1, 2, 1, 3, false}, {5, 5, 6, 6, true}} {
		from, to := e.Moved(c.first, c.last)
		if e.Keeps(c.first, c.last) != c.keeps || from != c.from || to != c.to {
			t.Errorf("lines %d-%d: keeps %v, moved to %d-%d", c.first, c.last, e.Keeps(c.first, c.last), from, to)
		}
	}
	if from, to := Diff("a\nb\nc", "a\nB\nc").Moved(2, 2); from != 2 || to != 2 {
		t.Errorf("a replaced line moved to %d-%d", from, to)
	}
	if (Edit{}).Keeps(1, 1) {
		t.Error("a removed file keeps its lines")
	}
}

// TestNeverMissed: whatever the change, lines Keeps reports kept read the
// same where Moved puts them, so a page citing changed lines is always
// written again; and every kept line is the same line.
func TestNeverMissed(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 500 {
		var a []string
		for range r.IntN(40) {
			a = append(a, string(rune('a'+r.IntN(5)))) // few letters: many equal lines to mismatch
		}
		b := slices.Clone(a)
		for range 1 + r.IntN(4) {
			i := r.IntN(len(b) + 1)
			switch r.IntN(3) {
			case 0:
				b = slices.Insert(b, i, string(rune('a'+r.IntN(6))))
			case 1:
				if i < len(b) {
					b = slices.Delete(b, i, i+1)
				}
			default:
				if i < len(b) {
					b[i] = string(rune('a' + r.IntN(6)))
				}
			}
		}
		before, after := strings.Join(a, "\n"), strings.Join(b, "\n")
		e, lines, now := Diff(before, after), strings.Split(before, "\n"), strings.Split(after, "\n")
		kept := 0 // the last kept line's number now: they stay in order
		for i, n := range e.to {
			if n > 0 && (lines[i] != now[n-1] || n <= kept) {
				t.Fatalf("Diff(%q, %q) kept line %d as %d: %v", before, after, i+1, n, e.to)
			}
			kept = max(kept, n)
		}
		for first := 1; first <= len(lines); first++ {
			for last := first; last <= len(lines); last++ {
				if from, to := e.Moved(first, last); e.Keeps(first, last) && !slices.Equal(lines[first-1:last], now[from-1:to]) {
					t.Fatalf("Diff(%q, %q) kept lines %d-%d as %d-%d", before, after, first, last, from, to)
				}
			}
		}
	}
}

// TestTouches: a citation of a manifest is touched only where the change broke
// its lines and the page says a word they lost; any other file's whenever it
// broke them, or the file was removed.
func TestTouches(t *testing.T) {
	pkg := Diff("{\n\"a\": \"1\",\n\"b\": \"2\"\n}", "{\n\"a\": \"1\",\n\"b\": \"3\"\n}")
	cargo := Diff("[deps]\nserde = \"1\"\ntokio = \"1.2.0\"\nrand = \"0.8\"", "[deps]\nserde = \"1\"\ntokio = \"1.3.0\"\nrand = \"0.8\"")
	code := Diff("a\nb\nc", "a\nB\nc")
	for _, c := range []struct {
		edit        Edit
		path        string
		first, last int
		text        string
		want        bool
	}{
		{pkg, "package.json", 1, 4, "b is pinned to 2", true}, {pkg, "package.json", 1, 4, "Lists a and b.", false}, {pkg, "package.json", 1, 2, "a 1", false},
		{cargo, "Cargo.toml", 1, 4, "It uses tokio.", false}, {cargo, "Cargo.toml", 1, 4, "It pins Tokio 1.2.0.", true}, {cargo, "Cargo.toml", 4, 4, "tokio 1.2.0", false},
		{code, "a.go", 1, 3, "", true}, {code, "a.go", 3, 3, "", false}, {Edit{}, "b.go", 1, 1, "", true}, {Edit{}, "go.mod", 1, 1, "", true},
	} {
		if got := c.edit.Touches(c.path, c.first, c.last, c.text); got != c.want {
			t.Errorf("Touches(%s, %d, %d, %q) = %v", c.path, c.first, c.last, c.text, got)
		}
	}
}

// TestLost: the words a change lost, line by line where as many lines
// replace them: a bumped version, a renamed dependency, two swapped versions;
// an insertion loses none.
func TestLost(t *testing.T) {
	for _, c := range []struct {
		before, after string
		gone          []string
	}{
		{"{\n  \"body-parser\": \"^2.3.0\",\n  \"cookie\": \"^0.7.1\"\n}", "{\n  \"body-parser\": \"^2.4.0\",\n  \"cookie\": \"^0.7.1\"\n}", []string{"2.3.0"}},
		{"<!-- deps -->\nserde = \"1\"", "<!-- dependencies -->\nserde_json = \"1\"", []string{"deps", "serde"}},
		{"\"a\": \"1.0.0\",\n\"b\": \"2.0.0\"", "\"a\": \"2.0.0\",\n\"b\": \"1.0.0\"", []string{"1.0.0", "2.0.0"}},
		{"x\nold = \"1\"\ny", "x\ny", []string{"old", "1"}},
		{"a\nc", "a\nb\nc", []string{}}, {"a", "a", []string{}},
	} {
		if got := Diff(c.before, c.after).gone; !slices.Equal(got, c.gone) || got == nil {
			t.Errorf("Diff(%q, %q) lost %q, want %q", c.before, c.after, got, c.gone)
		}
	}
}

// TestHome: a new file belongs with the groups citing the files nearest it,
// the root counting as a directory; with none where nothing near is cited.
func TestHome(t *testing.T) {
	groups := map[int][]string{0: {"README.md", "main.go"}, 1: {"src/api/a.go", "src/api/b.go"}, 5: {"src/store/s.go"}, 9: {"src/api/c.go", "test/a_test.go"}}
	for f, want := range map[string][]int{"src/api/new.go": {1, 9}, "src/web/w.go": {1, 5, 9}, "x.go": {0}, "docs/guide.md": nil, "test/b_test.go": {9}} {
		if got := slices.Sorted(maps.Keys(Home(f, groups))); !slices.Equal(got, want) {
			t.Errorf("Home(%s) = %v, want %v", f, got, want)
		}
	}
}
