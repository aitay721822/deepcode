package graph

import (
	"context"
	"fmt"
	"strings"
)

// A diagram stays legible in the wiki's column: at most maxNodes directories
// and maxEdges arrows, the busiest.
const (
	maxNodes = 14
	maxEdges = 24
	maxDepth = 3
)

// Diagram draws the imports between the snapshot's directories, tests,
// examples and docs aside, as a Mermaid flowchart, or returns "" when its
// files import none of each other's. It
// cuts directories to the most levels, at most maxDepth, that leave at most
// maxNodes of them linked; one level is drawn trimmed if even that leaves
// more. Long runs of folders a level folds are shortened in the labels.
func (g *Graph) Diagram(ctx context.Context, snap int64) (string, error) {
	edges, err := g.Store.Edges(ctx, snap)
	if err != nil || len(edges) == 0 {
		return "", err
	}
	t := levels(edges)
	var dirs []DirEdge
	for depth := maxDepth; depth >= 1; depth-- {
		if dirs = t.group(depth); len(nodes(dirs)) <= maxNodes {
			break
		}
	}
	for i, e := range dirs {
		dirs[i].From, dirs[i].To = t.short(e.From), t.short(e.To)
	}
	return draw(dirs), nil
}

// nodes lists the directories that edges link, in order of appearance.
func nodes(edges []DirEdge) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range edges {
		for _, d := range []string{e.From, e.To} {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// label escapes a directory for a quoted Mermaid label: a path may hold what
// Mermaid reads as markup or the label's end.
var label = strings.NewReplacer("#", "#35;", `"`, "#quot;", "<", "#lt;", ">", "#gt;", "`", "#96;")

// draw renders the busiest edges whose directories fit in maxNodes, top
// down, the root named "(root)" and the folders all share left out of the
// labels; "" when none link two directories.
func draw(edges []DirEdge) string {
	ids := map[string]string{}
	var names []string
	var arrows strings.Builder
	n := 0
	for _, e := range edges {
		if n == maxEdges {
			break
		}
		fresh := 0
		for _, d := range []string{e.From, e.To} {
			if ids[d] == "" {
				fresh++
			}
		}
		if len(ids)+fresh > maxNodes {
			continue
		}
		for _, d := range []string{e.From, e.To} {
			if ids[d] == "" {
				ids[d] = fmt.Sprintf("n%d", len(ids))
				names = append(names, d)
			}
		}
		fmt.Fprintf(&arrows, "  %s --> %s\n", ids[e.From], ids[e.To])
		n++
	}
	if n == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	trim := shared(names)
	for _, d := range names {
		name := strings.TrimPrefix(d, trim)
		if d == "." {
			name = "(root)"
		}
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", ids[d], label.Replace(name))
	}
	return b.String() + arrows.String()
}

// shared is the folders that all names begin with and none ends at, with a
// trailing slash; "" when the root is one of them.
func shared(names []string) string {
	common := strings.Split(names[0], "/")
	for _, n := range names {
		if n == "." {
			return ""
		}
		parts := strings.Split(n, "/")
		k := 0
		for k < len(common) && k < len(parts)-1 && common[k] == parts[k] {
			k++
		}
		common = common[:k]
	}
	if len(common) == 0 {
		return ""
	}
	return strings.Join(common, "/") + "/"
}
