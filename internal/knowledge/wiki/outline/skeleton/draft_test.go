package skeleton

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	aicontract "deepcode/internal/platform/ai/contract"
)

// sk is a skeleton of a guide with one child, then a file.
var sk = Skeleton{{Name: "Guide", Level: 2}, {Name: "Step", Level: 3}, {Name: "Router", Level: 2}}

// cited counts a text's "[ok]" as its citations that check out.
func cited(s string) int { return strings.Count(s, "[ok]") }

// TestParse: Markdown's text before its first ## or ### heading is the intro,
// each heading outside code blocks starts a section, #### stays in the text,
// and a heading the skeleton holds, case and backticks aside, takes the
// skeleton's name and level.
func TestParse(t *testing.T) {
	md := "Intro [ok].\n\n## guide\n\nG [ok].\n\n## `step`\n\nS.\n```sh\n## not a heading\n```\n#### Deep\n\nD.\n\n### Notes\n\nN.\n## Router\n"
	got := Parse(md, sk)
	want := Draft{Intro: "Intro [ok].", Sections: []Section{{"Guide", 2, "G [ok]."}, {"Step", 3, "S.\n```sh\n## not a heading\n```\n#### Deep\n\nD."},
		{"Notes", 3, "N."}, {"Router", 2, ""}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse =\n%+v\nwant\n%+v", got, want)
	}
	if got := Parse("```js``` is no fence\n\n## Router\n\nr", sk); got.Intro != "```js``` is no fence" || !reflect.DeepEqual(got.Sections, []Section{{"Router", 2, "r"}}) {
		t.Errorf("a backtick fence with a backtick in its info: %+v", got)
	}
	if r := Render(Draft{Intro: "I", Sections: []Section{{"A", 2, "a"}, {"B", 3, ""}}}); r != "I\n\n## A\n\na\n\n### B" {
		t.Errorf("Render = %q", r)
	}
}

// broken are vs's rules and wheres, "rule@where".
func broken(vs []aicontract.Violation) []string {
	var out []string
	for _, v := range vs {
		out = append(out, string(v.Rule)+"@"+v.Where)
	}
	return out
}

// TestCheck: a skeleton heading missing, with no text, or citing nothing that
// checks out is named, once, by its first fault, and so is a section the
// model added whose text cites nothing; the intro, a section with no text
// the model added, and a section that is only a diagram need no citation. A
// page with no skeleton citing nothing at all is named whole.
func TestCheck(t *testing.T) {
	d := Draft{Intro: "I", Sections: []Section{{"Guide", 2, ""}, {"Router", 2, "R"}, {"Extra", 2, ""}, {"More", 3, "m"}, {"Flow", 2, "```mermaid\ngraph TD\n```"}}}
	if got := broken(Check(sk, cited)(d)); !slices.Equal(got, []string{"write.empty@Guide", "write.heading@Step", "write.uncited@Router", "write.uncited@More"}) {
		t.Errorf("Check = %q", got)
	}
	if got := Check(sk, cited)(Draft{Intro: "I", Sections: []Section{{"Guide", 2, "g [ok]"}, {"Step", 3, "~~~~mermaid\ns\n~~~~"}, {"Router", 2, "r [ok]"}}}); len(got) != 0 {
		t.Errorf("a whole draft breaks %q", broken(got))
	}
	if got := Check(sk, cited)(Draft{Sections: []Section{{"Guide", 2, ""}, {"Step", 3, "s [ok]"}, {"Router", 2, "r [ok]"}}}); len(got) != 0 {
		t.Errorf("a heading opening onto its level-3 one breaks %q", broken(got))
	}
	if got := broken(Check(nil, cited)(Draft{Intro: "I", Sections: []Section{{"A", 2, "a"}}})); !slices.Equal(got, []string{"write.uncited@"}) {
		t.Errorf("an uncited page with no skeleton: %q", got)
	}
	if got := broken(Check(nil, cited)(Draft{Sections: []Section{{"A", 2, "a [ok]"}, {"B", 2, "b"}, {"C", 2, "```mermaid\nc\n```\nc"}, {"D", 2, "```go\nd\n```"}}})); !slices.Equal(got, []string{"write.uncited@B", "write.uncited@C", "write.uncited@D"}) {
		t.Errorf("a page with no skeleton: %q", got)
	}
}

// TestMerge: a repaired section replaces its first; a missing one goes after
// the last section of a heading before it, whatever follows; one the model
// added takes the repair's text where it is, at its own level; one the repair
// lacks stays as it was; a repair of the whole page replaces it. Fix drops a
// heading with no text, nor any under it, the skeleton's or the model's; Lacking
// names the skeleton's headings a draft lacks, and Gained those a skeleton
// gives that another did not.
func TestMerge(t *testing.T) {
	first := Draft{Intro: "I", Sections: []Section{{"Guide", 2, "g"}, {"Extra", 3, "e"}, {"Router", 2, "old"}}}
	fix := Draft{Sections: []Section{{"router", 2, "new"}, {"step", 2, "s"}, {"extra", 2, "e2"}}}
	vs := []aicontract.Violation{{Rule: Uncited, Where: "Router"}, {Rule: Missing, Where: "Step"}, {Rule: Empty, Where: "Guide"}, {Rule: Uncited, Where: "Extra"}}
	got := Merge(sk)(first, fix, vs)
	want := Draft{Intro: "I", Sections: []Section{{"Guide", 2, "g"}, {"Step", 3, "s"}, {"Extra", 3, "e2"}, {"Router", 2, "new"}}}
	if !reflect.DeepEqual(got, want) || first.Sections[2].Body != "old" || first.Sections[1].Body != "e" {
		t.Errorf("Merge =\n%+v\nwant\n%+v (first %+v)", got, want, first)
	}
	if got := Merge(sk)(Draft{}, Draft{Sections: []Section{{"Router", 2, "r"}}}, []aicontract.Violation{{Rule: Missing, Where: "Router"}}); !reflect.DeepEqual(got.Sections, []Section{{"Router", 2, "r"}}) {
		t.Errorf("into an empty draft: %+v", got)
	}
	whole := Draft{Intro: "W"}
	if got := Merge(nil)(first, whole, []aicontract.Violation{{Rule: Uncited}}); !reflect.DeepEqual(got, whole) {
		t.Errorf("a whole page's repair: %+v", got)
	}
	d := Draft{Sections: []Section{{"Guide", 2, ""}, {"Extra", 2, ""}, {"Router", 2, "r"}}}
	Fix(&d)
	if !reflect.DeepEqual(d.Sections, []Section{{"Router", 2, "r"}}) {
		t.Errorf("Fix left %+v", d.Sections)
	}
	if got := Lacking(sk, d); !slices.Equal(got, []string{"Guide", "Step"}) {
		t.Errorf("Lacking = %q", got)
	}
	d = Draft{Sections: []Section{{"Guide", 2, ""}, {"Step", 3, "s"}, {"Router", 2, ""}}}
	if Fix(&d); !reflect.DeepEqual(d.Sections, []Section{{"Guide", 2, ""}, {"Step", 3, "s"}}) {
		t.Errorf("Fix left %+v of a heading opening onto its level-3 one", d.Sections)
	}
	if got := show(Gained(sk[:2], Skeleton{{Name: "guide", Level: 2}, {Name: "`Step`", Level: 3}, {Name: "New", Level: 2}})); !slices.Equal(got, []string{"2 New @:0"}) {
		t.Errorf("Gained = %q", got)
	}
}

// TestNote: the repair names each section's fault, giving a range to cite
// when it cites nothing, and asks for those sections only, under their
// headings at their levels; a page citing nothing is asked for whole.
func TestNote(t *testing.T) {
	vs := []aicontract.Violation{{Rule: Missing, Where: "Step", Detail: "is missing"}, {Rule: Uncited, Where: "Router", Detail: "cites nothing that can be checked"}}
	md := Note(sk, "[a.go:1-9]")(vs)
	for _, want := range []string{`- "Step" is missing.`, `- "Router" cites nothing that can be checked: cite after each statement`, "as in [a.go:1-9].", "\n### Step\n## Router"} {
		if !strings.Contains(md, want) {
			t.Errorf("the note lacks %q:\n%s", want, md)
		}
	}
	if n := Note(nil, "[a.go:1-9]")([]aicontract.Violation{{Rule: Uncited}}); !strings.HasPrefix(n, "Your page has no citation that can be checked. Write the whole page again") {
		t.Errorf("a page citing nothing: %s", n)
	}
}

// TestListed: the request lists the headings at their levels, or says there
// are none, then the page's files when it has any.
func TestListed(t *testing.T) {
	if got := Listed(sk[:2], []string{"a.go", "b.go"}); got != "Its headings, in order:\n## Guide\n### Step\nIts files: a.go, b.go\n" {
		t.Errorf("Listed = %q", got)
	}
	if got := Listed(nil, nil); got != "The page has no listed headings.\n" {
		t.Errorf("no headings: %q", got)
	}
}
