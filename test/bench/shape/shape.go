// Package shape measures a wiki's plan for the bench, from its pages stored
// with their files: thin and crowded pages, and the projects and code files
// no page holds.
package shape

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/knowledge/wiki/store"
)

// CrowdedPages is how many pages' worth of lines crowd one page. An aside
// page past aside.crowded is the contract's count: code moves its extra units.
const CrowdedPages = 20

// Shape is a wiki's plan. A page planned before pages held their files, or
// one holding none (the overview, a section's head), is neither thin nor
// crowded.
type Shape struct {
	Pages    int      `json:"pages"`
	Thin     int      `json:"thinPages"`           // its files weigh under place.LinesPerPage
	Crowded  int      `json:"crowdedPages"`        // over CrowdedPages times that
	Homeless []string `json:"projectsWithoutPage"` // the projects no page covers
	Unnamed  []string `json:"-"`                   // the code units' code files on no page, reported with the topic coverage
}

// Measure is the shape of snapshot snap's wiki in db.
func Measure(ctx context.Context, db *sql.DB, snap int64) (Shape, error) {
	s := &store.Store{DB: db}
	paths, err := s.Paths(ctx, snap)
	if err != nil {
		return Shape{}, err
	}
	lines, err := s.Lines(ctx, snap)
	if err != nil {
		return Shape{}, err
	}
	pages, err := s.Pages(ctx, snap)
	if err != nil {
		return Shape{}, err
	}
	return Of(pages, paths, lines), nil
}

// Of is the shape of pages, planned for the files paths with their lines,
// weighed as place weighs units.
func Of(pages []store.Page, paths []string, lines map[string]int) Shape {
	sh := Shape{Pages: len(pages)}
	plan, covered := make([]outline.Page, len(pages)), map[string]bool{}
	for i, p := range pages {
		if p.Files == nil {
			continue
		}
		plan[i] = outline.Page{Chosen: p.Files.Chosen, Placed: p.Files.Placed}
		for _, u := range p.Files.Units {
			for _, k := range strings.Split(u, ", ") { // a merged unit's key joins its units' keys
				covered[k] = true
			}
		}
		if files := plan[i].Files(); len(files) == 0 {
			continue
		} else if w := place.Weigh(files, lines, true); w < place.LinesPerPage {
			sh.Thin++
		} else if w > CrowdedPages*place.LinesPerPage {
			sh.Crowded++
		}
	}
	code, _ := place.Raw(paths, lines, nil)
	var files []string
	for _, u := range code {
		files = append(files, u.Files...)
		if u.Kind == place.Project && !covered[u.Key] {
			sh.Homeless = append(sh.Homeless, u.Key)
		}
	}
	slices.Sort(files)
	sh.Unnamed = place.Unnamed(plan, files)
	return sh
}
