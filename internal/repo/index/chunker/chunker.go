// Package chunker splits a file into symbol-aware chunks for search and embedding.
package chunker

import (
	"strings"

	"deepcode/internal/repo/index/analyzer"
)

const (
	maxChunkLines = 120 // a symbol larger than this is split
	mergeLines    = 60  // neighbours are merged up to this size...
	minChunkLines = 20  // ...and a piece smaller than this joins a neighbour up to maxChunkLines
)

type Chunk struct {
	Symbol    string
	StartLine int
	EndLine   int
	Content   string
}

type span struct {
	start, end int
	name       string
}

// Chunks splits a file into chunks aligned to symbol boundaries. A symbol that
// fits stays whole; a large class, and any namespace, is split along its members,
// keeping the container's name on the pieces in between; any other large
// region is cut at blank lines. Small neighbours are then merged.
func Chunks(lines []string, symbols []analyzer.Symbol) []Chunk {
	var out []Chunk
	for _, s := range merge(plan(lines, symbols, 1, len(lines), "")) {
		content := strings.Join(lines[s.start-1:s.end], "\n")
		if strings.TrimSpace(content) == "" {
			continue
		}
		out = append(out, Chunk{Symbol: s.name, StartLine: s.start, EndLine: s.end, Content: content})
	}
	return out
}

// plan splits lines from to to into spans along the symbols' bounds.
func plan(lines []string, syms []analyzer.Symbol, from, to int, parent string) []span {
	var out []span
	cur := from
	for _, s := range syms {
		start, end := max(s.StartLine, cur), min(s.EndLine, to)
		if end < start {
			continue // nested inside the previous symbol (e.g. two declarations on one line)
		}
		if start > cur {
			out = append(out, split(lines, cur, start-1, parent)...)
		}
		name := s.QualifiedName(parent)
		switch {
		// A namespace is always split: a C# or Rust file is one, and a chunk
		// named after it would hide the names of the classes inside.
		case len(s.Children) > 0 && (s.Kind == "namespace" || end-start >= maxChunkLines):
			out = append(out, plan(lines, s.Children, start, end, name)...)
		case end-start < maxChunkLines:
			out = append(out, span{start, end, name})
		default:
			out = append(out, split(lines, start, end, name)...)
		}
		cur = end + 1
	}
	if cur <= to {
		out = append(out, split(lines, cur, to, parent)...)
	}
	return out
}

// split cuts [start,end] into pieces of at most maxChunkLines, preferring to
// end each piece on a blank line in its last third.
func split(lines []string, start, end int, name string) []span {
	var out []span
	for start <= end {
		stop := min(end, start+maxChunkLines-1)
		if stop < end {
			for i := stop; i > start+maxChunkLines*2/3; i-- {
				if strings.TrimSpace(lines[i-1]) == "" {
					stop = i
					break
				}
			}
		}
		out = append(out, span{start, stop, name})
		start = stop + 1
	}
	return out
}

// merge joins contiguous neighbours while the result stays small, and lets a
// tiny piece (a class header, a closing brace) join its neighbour.
func merge(spans []span) []span {
	var out []span
	for _, s := range spans {
		if n := len(out); n > 0 && out[n-1].end+1 == s.start {
			prev := &out[n-1]
			size := s.end - prev.start + 1
			tiny := prev.end-prev.start+1 < minChunkLines || s.end-s.start+1 < minChunkLines
			if size <= mergeLines || (tiny && size <= maxChunkLines) {
				prev.end = s.end
				prev.name = joinNames(prev.name, s.name)
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

// joinNames adds name b to the comma-separated list a, unless b ends it
// already or a holds five.
func joinNames(a, b string) string {
	switch {
	case b == "" || a == b || strings.HasSuffix(a, ", "+b):
		return a
	case a == "":
		return b
	case strings.Count(a, ",") >= 4:
		return a
	}
	return a + ", " + b
}
