package manifest

import (
	"slices"
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

// TestReplaced: the lines of the old version that the new one replaces; an
// insertion is the two lines around it, an edit adding or removing lines
// runs to the end, as the lines after it move, and only a citation sharing a
// line with them is touched.
func TestReplaced(t *testing.T) {
	for _, c := range []struct {
		before, after string
		want          Lines
	}{
		{"{\n  \"a\": \"1\",\n  \"b\": \"2\"\n}\n", "{\n  \"a\": \"1\",\n  \"b\": \"3\"\n}\n", Lines{3, 3}}, // one version bumped
		{"a\nc", "a\nb\nc", Lines{1, 2}},             // an insertion
		{"a\nb\nc", "a\nc", Lines{2, 3}},             // a deletion: c moves
		{"a\nb\nc\nd", "x\nb\nc\ny", Lines{1, 4}},    // two edits: one range
		{"a", "a\nb", Lines{1, 2}},                   // appended
		{"a\nb", "b", Lines{1, 2}},                   // first line removed
		{"a\nb\nc\nd", "a\nx\nb\nc\nd", Lines{1, 4}}, // an insertion: b to d move
	} {
		if got := Replaced(c.before, c.after); got != c.want {
			t.Errorf("Replaced(%q, %q) = %v, want %v", c.before, c.after, got, c.want)
		}
	}
	if same := Replaced("a\nb", "a\nb"); same.Covers(1, 2) { // a change of mode only
		t.Errorf("the same text replaced %v", same)
	}
	if l := (Lines{3, 3}); !l.Covers(1, 3) || l.Covers(4, 9) || l.Covers(1, 2) {
		t.Error("Covers")
	}
	if All("a\nb\n") != (Lines{1, 3}) {
		t.Error("All")
	}
}

// TestTouches: a citation of a manifest is touched only where the lines the
// edit replaced meet it and the page says a word they lost (any, when the
// lines after them moved), a manifest the change left alone not at all; any
// other file's, whenever the change affected it.
func TestTouches(t *testing.T) {
	affected := map[string]bool{"package.json": true, "Cargo.toml": true, "a.go": true}
	edits := map[string]Edit{"package.json": {Lines: Lines{3, 3}}, "Cargo.toml": {Lines{3, 3}, []string{"1.2.0"}}}
	for _, c := range []struct {
		path        string
		first, last int
		text        string
		want        bool
	}{
		{"package.json", 1, 5, "", true}, {"package.json", 3, 3, "", true}, {"package.json", 4, 9, "", false}, {"go.mod", 1, 9, "", false},
		{"Cargo.toml", 1, 9, "It uses tokio.", false}, {"Cargo.toml", 1, 9, "It pins Tokio 1.2.0.", true}, {"Cargo.toml", 4, 9, "tokio 1.2.0", false},
		{"a.go", 50, 60, "", true}, {"b.go", 1, 9, "", false},
	} {
		if got := Touches(c.path, c.first, c.last, c.text, affected, edits); got != c.want {
			t.Errorf("Touches(%s, %d, %d, %q) = %v", c.path, c.first, c.last, c.text, got)
		}
	}
}

// TestDiff: an edit that keeps the line count loses the words its old lines
// had and its new ones do not, as a bumped version; one that moves lines,
// nil: any citation of them is stale.
func TestDiff(t *testing.T) {
	bump := Diff("{\n  \"body-parser\": \"^2.3.0\",\n  \"cookie\": \"^0.7.1\"\n}", "{\n  \"body-parser\": \"^2.4.0\",\n  \"cookie\": \"^0.7.1\"\n}")
	if bump.Lines != (Lines{2, 2}) || len(bump.Gone) != 1 || bump.Gone[0] != "2.3.0" {
		t.Errorf("a bump: %+v", bump)
	}
	if renamed := Diff("<!-- deps -->\nserde = \"1\"", "<!-- dependencies -->\nserde_json = \"1\""); !slices.Equal(renamed.Gone, []string{"deps", "serde"}) {
		t.Errorf("a renamed dependency lost %q", renamed.Gone)
	}
	if swap := Diff("\"a\": \"1.0.0\",\n\"b\": \"2.0.0\"", "\"a\": \"2.0.0\",\n\"b\": \"1.0.0\""); len(swap.Gone) != 2 {
		t.Errorf("swapped versions lost %q", swap.Gone)
	}
	if added := Diff("a\nc", "a\nb\nc"); added.Gone != nil {
		t.Errorf("an insertion lost %q", added.Gone)
	}
	if same := Diff("a", "a"); same.Gone != nil || same.Covers(1, 1) {
		t.Errorf("the same text: %+v", same)
	}
}
