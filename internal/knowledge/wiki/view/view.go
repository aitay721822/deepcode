// Package view reads the wikis that wiki.Generator writes, as they are
// shown: the snapshot to show, its pages with their citations, each under its
// section, and the overview's diagram.
package view

import (
	"context"
	"errors"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/wiki/store"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/index/graph"
)

type Page struct {
	Slug      string              `json:"slug"`
	Title     string              `json:"title"`
	Content   string              `json:"content"`
	Citations []evidence.Citation `json:"citations"`
	// Parent is the slug of the section page this one is under, "" at the top.
	Parent string `json:"parent,omitempty"`
	// Diagram is a Mermaid flowchart of the imports between the snapshot's
	// directories, drawn from its graph rather than by the model: on the
	// overview, the first page, when its files import each other's.
	Diagram string `json:"diagram,omitempty"`
}

type Wiki struct {
	SnapshotID int64  `json:"snapshotId"`
	CommitSHA  string `json:"commitSha"`
	IndexedAt  string `json:"indexedAt"`
	Status     string `json:"status"`
	Pages      []Page `json:"pages"`
}

// Reader reads the wikis that wiki.Generator writes.
type Reader struct {
	Store *store.Store
	Graph *graph.Graph // the imports, drawn on the overview
}

// Load returns the wiki to show for a repository: the newest indexed
// snapshot with a finished wiki, else the published snapshot's pages so far,
// else the newest other indexed snapshot's pages so far, else an empty wiki
// with status "none". A pruned snapshot is never shown: its files are gone,
// so its citations could not be opened.
func (r Reader) Load(ctx context.Context, repoID int64) (Wiki, error) {
	w := Wiki{Status: catalog.WikiNone, Pages: []Page{}}
	shown, err := r.Store.Shown(ctx, repoID)
	if errors.Is(err, store.ErrNotFound) {
		return w, nil
	}
	if err != nil {
		return Wiki{}, err
	}
	w.SnapshotID, w.CommitSHA, w.IndexedAt, w.Status = shown.ID, shown.CommitSHA, shown.IndexedAt, shown.WikiStatus
	cites, err := r.Store.Citations(ctx, w.SnapshotID)
	if err != nil {
		return Wiki{}, err
	}
	pages, err := r.Store.Pages(ctx, w.SnapshotID)
	if err != nil || len(pages) == 0 {
		return w, err
	}
	slugs := map[int]string{} // by position
	for _, p := range pages {
		// Citations: [] rather than null in JSON.
		w.Pages = append(w.Pages, Page{Slug: p.Slug, Title: p.Title, Content: p.Content, Citations: append([]evidence.Citation{}, cites[p.ID]...)})
		slugs[p.Position] = p.Slug
	}
	for i, p := range pages {
		w.Pages[i].Parent = slugs[p.Parent]
	}
	// ponytail: drawn on every read, from all of the snapshot's edges; store it
	// with the snapshot if large repositories make wiki reads slow.
	w.Pages[0].Diagram, err = r.Graph.Diagram(ctx, w.SnapshotID)
	return w, err
}
