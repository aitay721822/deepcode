// Package evidence turns retrieved chunks into the source blocks a model reads,
// and checks the citations in its answer against exactly those blocks.
package evidence

import (
	"fmt"
	"strings"

	"deepcode/internal/knowledge/retrieval"
)

// Format renders hits as numbered source blocks for a prompt, stopping at
// maxChars; a single chunk longer than that is cut to its first lines. It
// returns the hits as shown: only those lines may be cited.
func Format(hits []retrieval.Hit, maxChars int) (string, []retrieval.Hit) {
	var b strings.Builder
	var used []retrieval.Hit
	for _, h := range hits {
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
		block += ">\n" + body.String() + "</source>\n\n"
		if b.Len()+len(block) > maxChars && len(used) > 0 {
			break
		}
		b.WriteString(block)
		used = append(used, h)
	}
	return b.String(), used
}
