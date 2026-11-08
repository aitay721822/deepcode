// Package change works out what a commit did to the files a wiki written
// before it cites: which lines survive and where they moved, so that only the
// pages citing lines the change replaced are written again and the others'
// citations follow their lines; and which pages a new file belongs with.
package change

import (
	"path"
	"slices"
	"strings"
)

// Edit is what a change did to one file's lines. The zero Edit is the file's
// removal: none of its lines survive.
type Edit struct {
	to   []int    // to[i] is line i+1's number after the change; 0 when the change replaced or removed it
	size int      // the file's lines after the change
	gone []string // the names and versions the replaced lines held that their replacements lack; nil for a removal
}

// maxEdits bounds the edits Diff looks for between the lines a change kept at
// either end. ponytail: past it the lines between count as replaced, which
// rewrites the pages citing them; raise it, or match unique lines first
// (patience diff), if a large refactor rewrites more pages than it should.
const maxEdits = 1000

// Diff is the edit after makes to before: the lines a shortest edit script
// keeps (Myers' algorithm) survive, moved by the lines added and removed
// before them; the rest are replaced.
func Diff(before, after string) Edit {
	a, b := strings.Split(before, "\n"), strings.Split(after, "\n")
	e := Edit{to: make([]int, len(a)), size: len(b)}
	head := 0
	for head < min(len(a), len(b)) && a[head] == b[head] {
		e.to[head] = head + 1
		head++
	}
	tail := 0
	for tail < min(len(a), len(b))-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		e.to[len(a)-1-tail] = len(b) - tail
		tail++
	}
	for _, m := range match(a[head:len(a)-tail], b[head:len(b)-tail]) {
		e.to[head+m[0]] = head + m[1] + 1
	}
	e.gone = lost(a, b, e.to)
	return e
}

// Keeps reports whether the change left lines first to last as they were, in
// one piece: none replaced, none added between them.
func (e Edit) Keeps(first, last int) bool {
	if first < 1 || last > len(e.to) || first > last {
		return false
	}
	for i := first; i <= last; i++ {
		if e.to[i-1] == 0 || e.to[i-1] != e.to[first-1]+i-first {
			return false
		}
	}
	return true
}

// Moved is where lines first to last are after the change: the same lines,
// moved, when Keeps holds; else from the line after the last one before them
// that survived to the line before the next one that did.
func (e Edit) Moved(first, last int) (int, int) {
	start, end := 1, e.size
	for i := min(first, len(e.to)); i >= 1; i-- {
		if t := e.to[i-1]; t > 0 {
			start = t
			if i < first {
				start++
			}
			break
		}
	}
	for i := max(last, 1); i <= len(e.to); i++ {
		if t := e.to[i-1]; t > 0 {
			end = t
			if i > last {
				end--
			}
			break
		}
	}
	start = min(start, max(e.size, 1))
	return start, max(start, end)
}

// Touches reports whether the change makes a page reading text, which cites
// lines first to last of file p, read differently: when those lines did not
// survive in one piece. For a manifest, only when the page also says a word
// the change lost, as the version a bump replaced: a page citing the list of
// dependencies as such still holds. ponytail: words, not meaning; a page
// naming the old version another way ("v2.3") keeps its text.
func (e Edit) Touches(p string, first, last int, text string) bool {
	if e.Keeps(first, last) {
		return false
	}
	if !Is(p) || e.gone == nil {
		return true
	}
	text = strings.ToLower(text) // "Tokio" names tokio
	return slices.ContainsFunc(e.gone, func(w string) bool { return strings.Contains(text, strings.ToLower(w)) })
}

// match pairs the lines of a and b that a shortest edit script keeps, as
// indexes into each; none when every script is longer than maxEdits.
func match(a, b []string) [][2]int {
	n, m := len(a), len(b)
	limit := min(n+m, maxEdits)
	o := limit + 1 // v[o+k] is how far along a diagonal k = x-y reaches
	v := make([]int, 2*limit+3)
	var trace [][]int // v after each round d, for k from -d to d
	for d := 0; d <= limit; d++ {
		for k := -d; k <= d; k += 2 {
			x := v[o+k-1] + 1 // a step right: a's line removed
			if k == -d || k != d && v[o+k-1] < v[o+k+1] {
				x = v[o+k+1] // a step down: b's line added
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[o+k] = x
			if x >= n && y >= m {
				return back(append(trace, slices.Clone(v[o-d:o+d+1])), n, m)
			}
		}
		trace = append(trace, slices.Clone(v[o-d:o+d+1]))
	}
	return nil
}

// back walks match's rounds back from the end, collecting the lines each
// round's run along its diagonal kept.
func back(trace [][]int, n, m int) [][2]int {
	var out [][2]int
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		px, py, sx, sy := 0, 0, 0, 0 // where round d's step began, and where its run did
		if d > 0 {
			at := func(k int) int { return trace[d-1][k+d-1] }
			k, pk := x-y, x-y-1
			if k == -d || k != d && at(k-1) < at(k+1) {
				pk = k + 1
			}
			px = at(pk)
			py = px - pk
			sx, sy = px+1, py
			if pk == k+1 {
				sx, sy = px, py+1
			}
		}
		for x > sx && y > sy {
			x, y = x-1, y-1
			out = append(out, [2]int{x, y})
		}
		x, y = px, py
	}
	slices.Reverse(out)
	return out
}

// lost is what the lines a change replaced held that their replacements
// lack: line by line where as many lines replace them (two swapped versions
// both count), else over the run of them.
func lost(a, b []string, to []int) []string {
	gone := []string{}
	for i, j := 0, 0; i < len(a); {
		if to[i] > 0 {
			i, j = i+1, to[i]
			continue
		}
		end := i
		for end < len(a) && to[end] == 0 {
			end++
		}
		next := len(b) // b's lines up to the next kept one replace a[i:end]
		if end < len(a) {
			next = to[end] - 1
		}
		old, cur := a[i:end], b[j:next]
		if len(old) != len(cur) {
			old, cur = []string{strings.Join(old, "\n")}, []string{strings.Join(cur, "\n")}
		}
		for k := range old {
			kept := words(cur[k])
			gone = append(gone, slices.DeleteFunc(words(old[k]), func(w string) bool { return slices.Contains(kept, w) })...)
		}
		i = end
	}
	return gone
}

// Home is where a new file f belongs among groups of a wiki's pages, given
// the files each group cites: the groups citing a file in the deepest
// directory f shares with any, at least its top one (the root counts as one);
// none when no group cites a file in f's top directory.
func Home(f string, groups map[int][]string) map[int]bool {
	best, out := 1, map[int]bool{}
	for g, files := range groups {
		depth := 0
		for _, c := range files {
			depth = max(depth, shared(f, c))
		}
		if depth > best {
			best, out = depth, map[int]bool{}
		}
		if depth == best {
			out[g] = true
		}
	}
	return out
}

// shared counts the leading directories two files' paths share, the root as one.
func shared(a, b string) int {
	x, y := strings.Split(path.Dir(a), "/"), strings.Split(path.Dir(b), "/")
	n := 0
	for n < min(len(x), len(y)) && x[n] == y[n] {
		n++
	}
	return n
}
