package loop

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
)

// Out is a hit the budget kept out of the evidence: evicted or refused by the
// call of Round and Tool (the first search's: round 1, "first"). From is the
// tool that had found an evicted hit.
type Out struct {
	retrieval.Hit
	Round int
	Tool  string
	From  string
}

// found is where a chunk held, or a range read, was first found: its Origin,
// and the call (the first search is call 0) and place among that call's hits
// the eviction order reads.
type found struct {
	Origin
	call, rank int
}

// admit adds h, found as f says, to the evidence when the evidence stays
// within its Limit with it (cost, summed). When it would not, chunks that
// other calls found are evicted until it does, in this order: by call, oldest
// first, so the first search's go first, then within a call the lowest ranked
// first. Ranges read are never evicted. When evicting all of them would not
// make room, none is evicted and h is refused: it is not added.
func (s *state) admit(h retrieval.Hit, f found) bool {
	undo := s.hold(h, f)
	hits, _ := s.evidence()
	chars, tokens := s.size(hits)
	over, overTokens := chars-s.req.Chars, tokens-s.req.Tokens
	fits := func() bool { return over <= 0 && (overTokens <= 0 || !s.req.Counts()) }
	if fits() {
		return true
	}
	var out []retrieval.Hit
	for _, c := range s.evictable(hits[len(s.reads):], f.call) {
		chars, tokens := s.cost(c)
		if out, over, overTokens = append(out, c), over-chars, overTokens-tokens; fits() {
			for _, c := range out {
				s.evict(c, f)
			}
			return true
		}
	}
	undo()
	s.res.Refused = append(s.res.Refused, Out{Hit: h, Round: f.Round, Tool: f.Tool})
	return false
}

// hold adds h to the reads, or as a chunk held to its tool's group, and
// returns what takes it back out.
func (s *state) hold(h retrieval.Hit, f found) (undo func()) {
	if f.Tool == "read_file" {
		s.reads, s.readFrom = append(s.reads, h), append(s.readFrom, f.Origin)
		return func() { s.reads, s.readFrom = s.reads[:len(s.reads)-1], s.readFrom[:len(s.readFrom)-1] }
	}
	g := s.group(f.Tool)
	s.held[h.ChunkID], *g = f, append(*g, h)
	return func() { delete(s.held, h.ChunkID); *g = (*g)[:len(*g)-1] }
}

// group is where the chunks tool finds are listed.
func (s *state) group(tool string) *[]retrieval.Hit {
	switch tool {
	case "first":
		return &s.first
	case "users", "follow":
		return &s.users
	}
	return &s.results
}

// evictable is the chunks of found, the evidence's past its reads, that a call
// other than call found, in the order they are evicted.
func (s *state) evictable(found []retrieval.Hit, call int) []retrieval.Hit {
	out := slices.DeleteFunc(slices.Clone(found), func(h retrieval.Hit) bool { return s.held[h.ChunkID].call == call })
	slices.SortStableFunc(out, func(a, b retrieval.Hit) int {
		fa, fb := s.held[a.ChunkID], s.held[b.ChunkID]
		return cmp.Or(cmp.Compare(fa.call, fb.call), cmp.Compare(fb.rank, fa.rank))
	})
	return out
}

// evict takes chunk c out of the evidence for the call by found: it is no
// longer held, so a later call may find it again.
func (s *state) evict(c retrieval.Hit, by found) {
	from := s.held[c.ChunkID].Tool
	delete(s.held, c.ChunkID)
	for _, g := range []*[]retrieval.Hit{&s.first, &s.users, &s.results} {
		*g = slices.DeleteFunc(*g, func(h retrieval.Hit) bool { return h.ChunkID == c.ChunkID })
	}
	s.res.Evicted = append(s.res.Evicted, Out{Hit: c, Round: by.Round, Tool: by.Tool, From: from})
}

// note tells the model what a call evicted and refused: Result's Evicted and
// Refused past the lengths they had before it.
func (s *state) note(evicted, refused int) string {
	var b strings.Builder
	if out := s.res.Evicted[evicted:]; len(out) > 0 {
		fmt.Fprintf(&b, "To make room, these left the evidence: %s. A later call may find them again.\n", s.ranges(out, false))
	}
	if out := s.res.Refused[refused:]; len(out) > 0 {
		hits, _ := s.evidence()
		fmt.Fprintf(&b, "Not added, over the evidence budget: %s; %s. Read a narrower range, or call done.\n", s.ranges(out, true), s.left(s.size(hits)))
	}
	return b.String()
}

// ranges names hits by path and lines, with their sizes in the evidence when
// sizes says.
func (s *state) ranges(out []Out, sizes bool) string {
	parts := make([]string, len(out))
	for i, o := range out {
		parts[i] = fmt.Sprintf("%s:%d-%d", o.Path, o.StartLine, o.EndLine)
		if sizes {
			parts[i] += " (" + s.units(s.cost(o.Hit)) + ")"
		}
	}
	return strings.Join(parts, ", ")
}

// units names chars bytes and tokens tokens, each when the budget bounds it:
// bytes unless a deep answer's evidence takes the room its context leaves,
// tokens when it Counts.
func (s *state) units(chars, tokens int) string {
	var parts []string
	if s.req.Chars < math.MaxInt {
		parts = append(parts, fmt.Sprintf("%d bytes", chars))
	}
	if s.req.Counts() {
		parts = append(parts, fmt.Sprintf("%d tokens", tokens))
	}
	return strings.Join(parts, ", ")
}

// left is what the budget has left past evidence of chars bytes and tokens
// tokens, in the units it bounds.
func (s *state) left(chars, tokens int) string {
	var parts []string
	if s.req.Chars < math.MaxInt {
		parts = append(parts, fmt.Sprintf("%d bytes left of %d", s.req.Chars-chars, s.req.Chars))
	}
	if s.req.Counts() {
		parts = append(parts, fmt.Sprintf("%d tokens left of %d", s.req.Tokens-tokens, s.req.Tokens))
	}
	return strings.Join(parts, ", ")
}

// cost is h's block in the evidence (evidence.Block): its bytes, and its
// tokens when the budget Counts them, each block counted once.
func (s *state) cost(h retrieval.Hit) (chars, tokens int) {
	block := evidence.Block(h)
	if !s.req.Counts() {
		return len(block), 0
	}
	n, ok := s.tokens[block]
	if !ok {
		n = s.req.Count(block)
		s.tokens[block] = n
	}
	return len(block), n
}

// size is hits' cost, summed: the evidence as evidence.Format renders it, whole.
func (s *state) size(hits []retrieval.Hit) (chars, tokens int) {
	for _, h := range hits {
		c, t := s.cost(h)
		chars, tokens = chars+c, tokens+t
	}
	return chars, tokens
}
