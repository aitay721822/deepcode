package mermaid

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestCheck holds Check to what node's mermaid.parse (12.2.0, the version
// web/ draws with) says of each diagram: a model's usual slips fail, with the
// parser's own words, and every diagram Mermaid reads passes.
func TestCheck(t *testing.T) {
	for _, c := range []struct {
		name, code string
		ok         bool
		says       string // in the error, when it fails
	}{
		{"flowchart, quoted labels", "flowchart TD\n  A[\"Start (init)\"] --> B{\"Is it ok?\"}\n  B -->|yes| C[\"Done: ok\"]\n  B -->|no| D[\"Retry <br> again\"]", true, ""},
		{"sequence with alt", "sequenceDiagram\n  participant C as Client\n  participant S as Server\n  C->>S: GET /x\n  alt found\n    S-->>C: 200\n  else missing\n    S-->>C: 404\n  end", true, ""},
		{"unquoted label with parentheses", "flowchart TD\n  A[foo(bar)] --> B", false, "got 'PS'"},
		{"quotes inside a label", "flowchart TD\n  A --> B[label \"with\" quotes]", false, "got 'STR'"},
		{"alt with no end", "sequenceDiagram\n  A->>B: hi\n  alt ok\n    B-->>A: yes\n  else no\n    B-->>A: no", false, "Parse error on line 7"},
		{"unquoted label with <br>", "flowchart TD\n  A[first line<br>second] --> B", true, ""},
		{"participant with a colon", "sequenceDiagram\n  participant A: Alice\n  A->>A: hi", false, "got 'INVALID'"},
		{"graph with a link's text", "graph LR\n  A -- text --> B", true, ""},
		{"unknown diagram type", "flowdiagram TD\n  A --> B", false, "No diagram type detected"},
		{"empty", "", false, "No diagram type detected"},
		{"classDiagram", "classDiagram\n  class Repo {\n    +Name string\n    +Index() error\n  }\n  Repo <|-- GitRepo", true, ""},
		{"erDiagram", "erDiagram\n  USER ||--o{ ORDER : places", true, ""},
		{"stateDiagram-v2", "stateDiagram-v2\n  [*] --> Idle\n  Idle --> Busy: start\n  Busy --> [*]", true, ""},
		{"init directive", "%%{init: {\"theme\": \"dark\"}}%%\nflowchart LR\n  A --> B", true, ""},
		{"front matter", "---\ntitle: Login\n---\nsequenceDiagram\n  A->>B: hi", true, ""},
		{"broken front matter", "---\ntitle: [unclosed\n---\nflowchart LR\n  A --> B", false, "unexpected end of the stream"},
		{"pie (structuredClone)", "pie title Pets\n  \"Dogs\" : 386\n  \"Cats\" : 85", true, ""},
		{"radar", "radar-beta\n  axis a, b, c\n  curve x{1, 2, 3}", true, ""},
	} {
		err := Check(c.code)
		if errors.Is(err, ErrUnbuilt) {
			t.Fatal(err)
		}
		if (err == nil) != c.ok || err != nil && !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: Check = %v, want ok %v saying %q", c.name, err, c.ok, c.says)
		}
	}
}

// TestStuck: a parse cut short by the limit leaves Mermaid's queue busy, so
// the next Check builds a new runtime and reads a diagram again; an error that
// quotes a long diagram is cut short.
func TestStuck(t *testing.T) {
	huge := "flowchart TD\n  A --> B" + strings.Repeat(" & C", 200000)
	limit = 50 * time.Millisecond
	err := Check(huge)
	limit = 5 * time.Second
	if err == nil {
		t.Fatal("a parse past the limit passed")
	}
	if err := Check("flowchart TD\n  A --> B"); err != nil {
		t.Errorf("after a parse past the limit: %v", err)
	}
	if err := Check("nodiagram " + strings.Repeat("x", 5000)); err == nil || len(err.Error()) > most+len("…") {
		t.Errorf("a long error: %d bytes", len(err.Error()))
	}
}
