package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// replying is a Chat that gives every Ask r: the part of the contract the free functions keep.
type replying struct {
	Chat
	r Reply
}

// Name is m.
func (replying) Name() string { return "m" }

// SchemaMode is SchemaPrompt.
func (replying) SchemaMode() string { return SchemaPrompt }

// Ask gives r.
func (c replying) Ask(context.Context, Prompt, *JSONSchema, func(string)) (Reply, error) {
	return c.r, nil
}

// TestSpent: an answer that stopped at the output budget with nothing said fails Stream; JSON it cut fails StreamJSON
// naming the budget, not the schema; JSON that ended before it is read.
func TestSpent(t *testing.T) {
	f := Schema[struct {
		A int `json:"a"`
	}]("f")
	_, empty := Stream(context.Background(), replying{r: Reply{Text: "  ", Spent: true}}, Prompt{}, nil)
	_, _, cut := StreamJSON(context.Background(), replying{r: Reply{Text: `{"a":`, Spent: true}}, Prompt{}, f, nil)
	v, _, whole := StreamJSON(context.Background(), replying{r: Reply{Text: `{"a":1}`, Spent: true}}, Prompt{}, f, nil)
	if empty == nil || !strings.Contains(empty.Error(), "whole output budget") || cut == nil || !strings.HasPrefix(cut.Error(), "m: the answer reached the output budget") ||
		errors.Is(cut, ErrSchema) || whole != nil || v.A != 1 {
		t.Errorf("empty: %v; cut: %v; whole: %v %+v", empty, cut, whole, v)
	}
}

// TestPieces: Size counts each piece once, the part the last request sent apart, and a replaced piece again; Added
// is the pieces after those sent.
func TestPieces(t *testing.T) {
	var p Pieces
	counted := 0
	count := func(s string) int { counted++; return len(s) }
	p.Add("ab")
	p.MarkSent(1)
	if p.Add("cde") != 1 || p.Added() != 1 || p.Chars() != 5 || strings.Join(p.Texts(1), "|") != "ab" {
		t.Fatalf("added %d, %d characters, sent %q", p.Added(), p.Chars(), p.Texts(1))
	}
	p.Size(count)
	if next, sent := p.Size(count); next != 5 || sent != 2 || counted != 2 {
		t.Errorf("size %d, sent %d, counted %d", next, sent, counted)
	}
	if p.Replace(1, "x"); p.Piece(1) != "x" {
		t.Errorf("replaced: %q", p.Piece(1))
	}
	if next, _ := p.Size(count); next != 3 || counted != 3 {
		t.Errorf("after the replace: size %d, counted %d", next, counted)
	}
}

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

// TestThrottleForgetsAbandoned: callers that give up waiting for their turn
// leave no gap behind, so the next caller is not pushed past its timeout; turns
// stay spaced, and a caller that gave up before it came takes none.
func TestThrottleForgetsAbandoned(t *testing.T) {
	var th Throttle // 600 a minute: a turn every 100ms
	stopped, stop := context.WithCancel(context.Background())
	stop()
	if th.Wait(stopped, 600) == nil {
		t.Fatal("a caller that had given up took the free turn")
	}
	gone, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	for range 20 { // one gets the first turn; the rest give up
		wg.Go(func() { th.Wait(gone, 600) })
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := th.Wait(ctx, 600); err != nil {
		t.Fatalf("the caller after 19 abandoned ones: %v", err)
	}
	start := time.Now()
	if err := th.Wait(ctx, 600); err != nil || time.Since(start) < 50*time.Millisecond {
		t.Fatalf("the next turn came after %v: %v", time.Since(start), err)
	}
}
