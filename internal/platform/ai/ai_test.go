package ai

import (
	"fmt"
	"testing"
	"unicode/utf8"
)

func TestPrompt(t *testing.T) {
	read := func(p Prompt) (s string) {
		for _, m := range p.Messages() {
			s += m.Role + ":" + m.Content + " "
		}
		return s
	}
	history := append(make([]Message, 0, 4), Message{Role: "user", Content: "earlier"}) // room to grow, which After must not write into
	p := Prompt{Static: "rules", Previous: history, Dynamic: "now"}
	first, second := p.After("one", "again"), p.After("two", "once more")
	for _, c := range [][2]string{{read(p), "system:rules user:earlier user:now "}, {read(Prompt{Previous: history}) + "|" + read(Prompt{}), "user:earlier |"}, {fmt.Sprintln(p.Size(utf8.RuneCountInString)) + fmt.Sprint(first.Size(utf8.RuneCountInString)), "15 3\n23 5"},
		{read(first), "system:rules user:earlier user:now assistant:one user:again "}, // second did not overwrite it
		{read(second.After("three", "last")), "system:rules user:earlier user:now assistant:two user:once more assistant:three user:last "}} {
		if c[0] != c[1] {
			t.Errorf("got %q, want %q", c[0], c[1])
		}
	}
}
