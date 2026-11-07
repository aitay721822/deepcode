package graph

import (
	"context"
	"database/sql"
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
// cuts directories to the most segments, at most maxDepth, that leave at
// most maxNodes of them linked; one segment is drawn trimmed if even that
// leaves more.
func Diagram(ctx context.Context, db *sql.DB, snap int64) (string, error) {
	edges, err := loadEdges(ctx, db, snap)
	if err != nil || len(edges) == 0 {
		return "", err
	}
	var dirs []DirEdge
	for depth := maxDepth; depth >= 1; depth-- {
		if dirs = group(edges, depth); len(nodes(dirs)) <= maxNodes {
			break
		}
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
// down, the root named "(root)"; "" when none link two directories.
func draw(edges []DirEdge) string {
	ids := map[string]string{}
	var decl, arrows strings.Builder
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
				name := d
				if d == "." {
					name = "(root)"
				}
				fmt.Fprintf(&decl, "  %s[\"%s\"]\n", ids[d], label.Replace(name))
			}
		}
		fmt.Fprintf(&arrows, "  %s --> %s\n", ids[e.From], ids[e.To])
		n++
	}
	if n == 0 {
		return ""
	}
	return "flowchart TD\n" + decl.String() + arrows.String()
}
