// Package evidence turns retrieved chunks into the source blocks a model reads,
// and checks the citations in its answer against exactly those blocks.
package evidence

import (
	"fmt"
	"math"
	"strings"

	"deepcode/internal/knowledge/retrieval"
)

// Limit bounds the evidence: Chars bytes as Format renders it, and Tokens of
// Count's tokens when it Counts.
type Limit struct {
	Chars, Tokens int
	Count         func(string) int
}

// Counts reports whether l bounds tokens: Count is set and Tokens is below
// Chars. A model's token is a byte or more, so Tokens of Chars or more bounds
// nothing Chars does not.
func (l Limit) Counts() bool { return l.Count != nil && l.Tokens < l.Chars }

// Format renders hits as numbered source blocks for a prompt, stopping at l;
// a single chunk longer than that is cut to its first lines (to l.Tokens bytes
// for its tokens, as many as fit or fewer). It returns the hits as shown: only
// those lines may be cited; and, when l has a Count, the blocks' tokens, each
// block counted alone, whether or not l Counts them.
func Format(hits []retrieval.Hit, l Limit) (text string, used []retrieval.Hit, tokens int) {
	var b strings.Builder
	for _, h := range hits {
		block, shown := render(h, l.Chars)
		n := 0
		if l.Count != nil {
			if n = l.Count(block); n > l.Tokens && l.Counts() && len(used) == 0 {
				block, shown = render(h, l.Tokens)
				n = l.Count(block)
			}
		}
		if (b.Len()+len(block) > l.Chars || tokens+n > l.Tokens && l.Counts()) && len(used) > 0 {
			break
		}
		b.WriteString(block)
		used, tokens = append(used, shown), tokens+n
	}
	return b.String(), used, tokens
}

// Block is h's block as Format writes it, uncut: hits whose Blocks' bytes sum
// to at most l.Chars are all shown, whole, when their tokens sum to at most
// l.Tokens too.
func Block(h retrieval.Hit) string {
	block, _ := render(h, math.MaxInt)
	return block
}

// render is h's block, its lines cut past maxChars but the first, and h as
// shown.
func render(h retrieval.Hit, maxChars int) (string, retrieval.Hit) {
	// Repository text is untrusted: keep it from closing the block early.
	content := strings.ReplaceAll(h.Content, "</source", "<\\/source")
	var body strings.Builder
	last := h.StartLine
	for i, l := range strings.Split(content, "\n") {
		line := fmt.Sprintf("%d: %s\n", h.StartLine+i, l)
		if i > 0 && body.Len()+len(line) > maxChars {
			break
		}
		body.WriteString(line)
		last = h.StartLine + i
	}
	h.EndLine = min(h.EndLine, last)
	block := fmt.Sprintf("<source path=%q lines=\"%d-%d\"", h.Path, h.StartLine, h.EndLine)
	if h.Symbol != "" {
		block += fmt.Sprintf(" symbol=%q", h.Symbol)
	}
	return block + ">\n" + body.String() + "</source>\n\n", h
}
