package codemap

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"deepcode/internal/knowledge/retrieval"
)

// maxTraces and maxLocations bound a map however long the model runs on;
// locations are lettered a to z.
const (
	maxTraces    = 6
	maxLocations = 10
)

// ground keeps the locations whose line the sources show, and numbers what is
// kept (1, 1a, 1b, 2, ...). A location keeps its line when that line reads as
// its code says; else it moves to the nearest line of its file that does. One
// without code, whose file or code the sources lack, or that repeats a line of
// its trace, is dropped, and so is a trace left without locations.
func ground(m *Map, hits []retrieval.Hit) {
	shown := map[string]map[int]string{} // path, line number: text
	for _, h := range hits {
		if shown[h.Path] == nil {
			shown[h.Path] = map[int]string{}
		}
		for i, text := range strings.Split(h.Content, "\n") {
			if n := h.StartLine + i; n <= h.EndLine {
				shown[h.Path][n] = text
			}
		}
	}
	var traces []Trace
	for _, t := range m.Traces {
		var kept []Location
		for _, l := range t.Locations {
			l.Path = resolve(l.Path, shown)
			line, ok := find(shown[l.Path], l.Line, norm(l.Code))
			if !ok { // the code may hold the number the sources put before its line
				line, ok = find(shown[l.Path], l.Line, norm(numberRe.ReplaceAllString(l.Code, "")))
			}
			if ok && len(kept) < maxLocations && !slices.ContainsFunc(kept, func(k Location) bool { return k.Path == l.Path && k.Line == line }) {
				l.Line, l.Code = line, strings.TrimSpace(shown[l.Path][line])
				l.ID = strconv.Itoa(len(traces)+1) + string(rune('a'+len(kept)))
				kept = append(kept, l)
			}
		}
		if len(kept) > 0 && len(traces) < maxTraces {
			t.ID, t.Locations = strconv.Itoa(len(traces)+1), kept
			traces = append(traces, t)
		}
	}
	m.Traces = traces
}

// numberRe matches the line number the sources put before each line (12: ),
// which a model may copy into a location's code.
var numberRe = regexp.MustCompile(`^\s*\d+:\s`)

// resolve is path as the sources name it: itself, or the one source path
// that ends with it (a model may drop a leading folder).
func resolve(path string, shown map[string]map[int]string) string {
	path = strings.TrimPrefix(strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(path), `\`, "/"), "./"), "/")
	if _, ok := shown[path]; ok || path == "" {
		return path
	}
	var match string
	for p := range shown {
		if strings.HasSuffix(p, "/"+path) {
			if match != "" {
				return path // two files end so: neither is meant
			}
			match = p
		}
	}
	return match
}

// find is the line of lines that code is: line itself when it reads so, else
// the nearest line that does.
func find(lines map[int]string, line int, code string) (int, bool) {
	if code == "" {
		return 0, false
	}
	if reads(lines[line], code) {
		return line, true
	}
	best, found := 0, false
	for n, text := range lines {
		if reads(text, code) && (!found || abs(n-line) < abs(best-line) || abs(n-line) == abs(best-line) && n < best) {
			best, found = n, true
		}
	}
	return best, found
}

// reads reports whether text, a source line, is the line code copies: the
// same but for spacing, or holding all of it as at least half the line (a
// trailing comment or brace left out), or all of text when the model ran two
// lines together. A short fragment (a brace, a bare return) matches only a
// line that is just that.
func reads(text, code string) bool {
	text = norm(text)
	if len(text) < 4 {
		return text != "" && text == code
	}
	return text == code || len(code) >= 6 && 2*len(code) >= len(text) && strings.Contains(text, code) ||
		strings.Contains(code, text) && len(text) >= 12
}

func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

func abs(n int) int { return max(n, -n) }
