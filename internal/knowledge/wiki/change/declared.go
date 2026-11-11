package change

import (
	"slices"

	"deepcode/internal/repo/index/indexer/chunker"
)

// Novel reports whether a chunk named name declares something none of the
// chunks named held did. A chunk's name lists the declarations merged into
// it (chunker.Names), and an edit that moves where chunks split ("A, B"
// becoming "A" and "B") declares nothing new; nor does code outside any
// declaration. ponytail: a chunk name lists five declarations at most, so a
// sixth that comes to head a chunk of its own reads as new; compare the
// symbols table if that rewrites pages needlessly.
func Novel(name string, held map[string]bool) bool {
	known := map[string]bool{}
	for n := range held {
		for _, d := range chunker.Names(n) {
			known[d] = true
		}
	}
	return slices.ContainsFunc(chunker.Names(name), func(d string) bool { return !known[d] })
}
