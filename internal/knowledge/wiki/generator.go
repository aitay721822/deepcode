// Package wiki generates a DeepWiki-style set of pages for a snapshot: the
// model plans them, in sections of the code that imports itself most, sized
// by the repository (or .devin/wiki.json lists them), then writes each page
// from retrieved evidence with checked citations, Parallel at a time.
// A new snapshot carries over the pages of the previous wiki that its change
// does not touch.
package wiki

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/knowledge/wiki/change"
	"deepcode/internal/knowledge/wiki/fanout"
	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/steering"
	"deepcode/internal/knowledge/wiki/store"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/gitserver"
	"deepcode/internal/repo/index/graph"
)

type Generator struct {
	Store     *store.Store
	Graph     *graph.Graph // the imports: how the code divides into sections, and what a change reaches
	Catalog   *catalog.Catalog
	Retriever *retrieval.Retriever
	Chat      *ai.OpenAI
	Language  string
	Parallel  int // pages planned or written at a time; 0 is one
	MaxPages  int // the most pages a wiki gets; 0 is defaultMaxPages
	// Planned, when set, is told how each plan the model makes went. The bench
	// sets it.
	Planned func(Planning)
}

const (
	defaultMaxPages = 64 // enough for DeepWiki's page counts on the bench's repositories
	flatPages       = 8  // the most pages of a wiki too small for sections
	pageHits        = 12
	evidenceChars   = 48000 // a page's sources in all
	sectionChars    = 24000 // of those, the ones a section's pages share
)

// source is where the wiki's citations link: a commit of a repository on its git server.
type source struct {
	url, kind, sha string
}

type (
	page    = store.Page    // a stored page
	newPage = store.NewPage // a page to store
)

// Run generates the wiki of the READY snapshot of repoID at sha. Its pages are
// stored before any is written, so a retry resumes with the pages left.
func (g *Generator) Run(ctx context.Context, repoID int64, sha string, progress jobs.Progress) (err error) {
	r, err := g.Catalog.Get(ctx, repoID)
	if errors.Is(err, catalog.ErrNotFound) {
		return jobs.Permanent(err)
	} else if err != nil {
		return err
	}
	at, err := g.Store.Snapshot(ctx, repoID, sha)
	if errors.Is(err, store.ErrNotFound) {
		return jobs.Permanent(fmt.Errorf("snapshot %s is gone", sha))
	} else if err != nil {
		return err
	}
	snap := at.ID
	src := source{url: r.URL, kind: r.Provider, sha: sha}
	if at.Status != "ready" || at.WikiStatus == catalog.WikiReady { // a re-index republishes a snapshot whose wiki is written
		progress.Logf("snapshot %.7s is %s with a %s wiki: nothing to generate", sha, at.Status, at.WikiStatus)
		return nil
	}
	g.setStatus(ctx, snap, catalog.WikiGenerating)
	defer func() {
		if v := recover(); v != nil { // turned into the job's error, so the wiki can be retried
			err = fmt.Errorf("panic: %v", v)
		}
		if err != nil {
			g.setStatus(ctx, snap, catalog.WikiFailed)
		}
	}()

	if at.Language != g.Language {
		// Pages stored in another language (DEEPCODE_WIKI_LANGUAGE changed since,
		// or they predate wiki_language) are dropped and planned again, so no wiki
		// mixes two languages.
		if err := g.Store.Drop(ctx, snap); err != nil {
			return err
		}
	}
	repo := r.Owner + "/" + r.Name
	steer, err := g.steer(ctx, catalog.Ref{ID: snap, CommitSHA: sha}, progress)
	if err != nil {
		return err
	}
	pages, err := g.plannedPages(ctx, repoID, snap, src, repo, steer, progress)
	if err != nil {
		return err
	}
	// The top pages first, sections' among them, then the pages under those:
	// a section's shared sources are in the model server's prompt cache by
	// the time its pages are written.
	var top, under []page
	for _, p := range pages {
		if p.Content == "" && p.Parent < 0 {
			top = append(top, p)
		} else if p.Content == "" {
			under = append(under, p)
		}
	}
	var mu sync.Mutex
	done := len(pages) - len(top) - len(under)
	progress.Stage("write", done, len(pages))
	for _, todo := range [][]page{top, under} {
		if err := fanout.Each(ctx, len(todo), g.Parallel, func(ctx context.Context, i int) error {
			p := todo[i]
			log := progress.With("[" + p.Title + "] ") // pages written at a time interleave their lines
			log.Logf("== page %d of %d", p.Position+1, len(pages))
			content, cites, err := g.write(ctx, snap, repo, src, p, pages, steer.Notes, log)
			if err != nil {
				return err
			}
			if err := g.Store.Save(ctx, p.ID, content, cites); err != nil {
				return err
			}
			log.Logf("== saved with %d checked citations", len(cites))
			mu.Lock()
			defer mu.Unlock()
			done++
			progress.Stage("write", done, len(pages))
			return nil
		}); err != nil {
			return err
		}
	}
	g.setStatus(ctx, snap, catalog.WikiReady)
	return nil
}

// setStatus records wiki progress on the snapshot; it runs even when ctx was
// cancelled, so a failed run is never left "generating".
func (g *Generator) setStatus(ctx context.Context, snap int64, s string) {
	if err := g.Store.SetStatus(context.WithoutCancel(ctx), snap, s); err != nil {
		log.Printf("wiki: status: %v", err)
	}
}

// store saves the snapshot's pages, in order, each keyed by its slug, and
// the language they are written in, in one transaction: a resumed job must
// not take half a plan for the whole.
func (g *Generator) store(ctx context.Context, snap int64, pages []newPage) error {
	for i := range pages {
		pages[i].Slug = slug(i, pages[i].Title)
	}
	return g.Store.Add(ctx, snap, g.Language, pages)
}

// carry starts the snapshot's wiki from the repository's newest finished wiki
// in the same language: the same pages, each keeping its text unless the
// change may touch it, so only the touched pages are written again; a kept
// page's citations follow their lines, and its links go to the new commit. It
// reports false, storing nothing, when there is no such wiki, when
// .devin/wiki.json changed (it may list other pages), when more than half of
// the files changed, when the change adds a directory, two levels deep as
// planning sees them, that code outside it imports (a new subsystem, which
// only a new plan gives a page), or, unless .devin/wiki.json lists the pages,
// when the wiki is flat and the repository now calls for sections, or the
// other way round.
func (g *Generator) carry(ctx context.Context, repoID, snap int64, src source, listed bool, progress jobs.Progress) (bool, error) {
	base, baseSHA, err := g.Store.Base(ctx, repoID, snap, g.Language)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	changed, files, err := g.Catalog.Changed(ctx, base, snap)
	if err != nil {
		return false, err
	}
	if changed[steering.File] || 2*len(changed) > files {
		progress.Logf("%d of %d files changed since %.7s, or %s did: planning anew", len(changed), files, baseSHA, steering.File)
		return false, nil
	}
	added, err := g.Graph.NewDirs(ctx, base, snap, 2)
	if err != nil {
		return false, err
	}
	if len(added) > 0 {
		progress.Logf("since %.7s the code imports the new %s: planning anew", baseSHA, strings.Join(added, ", "))
		return false, nil
	}
	// A page may also read differently when a file it cites imports a
	// declaration the change altered.
	ripple, err := g.Graph.Impact(ctx, base, snap, changed)
	if err != nil {
		return false, err
	}
	old, err := g.Store.Pages(ctx, base)
	if err != nil {
		return false, err
	}
	if !listed { // a wiki planned flat, or before sections, gets them as the repository grows into them
		n, err := g.Store.FileCount(ctx, snap)
		if err != nil {
			return false, err
		}
		// More pages than a flat plan holds: sections whose pages the model all failed to plan.
		// ponytail: 8 such pages or fewer read as flat and are planned anew, which may fill them;
		// store the shape if a model that keeps failing section plans replans every snapshot.
		sectioned := len(old) > flatPages || slices.ContainsFunc(old, func(p page) bool { return p.Parent >= 0 })
		if !outline.Fits(sectioned, n, cmp.Or(g.MaxPages, defaultMaxPages)) {
			progress.Logf("the wiki of %.7s is no longer the shape %d files call for: planning anew", baseSHA, n)
			return false, nil
		}
	}
	cites, err := g.Store.Citations(ctx, base)
	if err != nil {
		return false, err
	}
	u, err := g.weigh(ctx, base, snap, changed, ripple, old, cites)
	if err != nil {
		return false, err
	}
	link := func(sha string, c evidence.Citation) string {
		return evidence.Link(c, gitserver.SourceURL(src.kind, src.url, sha, c.Path, c.StartLine, c.EndLine))
	}
	pages := make([]newPage, len(old))
	kept := 0
	for i, p := range old {
		pages[i] = newPage{Title: p.Title, Query: p.Query, Parent: p.Parent}
		touched, err := g.touched(ctx, snap, p, cites[p.ID], u)
		if err != nil {
			return false, err
		} else if touched {
			continue
		}
		// The cited lines read as they did: the citations follow them, the links to the new commit.
		now, swap := slices.Clone(cites[p.ID]), []string{}
		for j, c := range cites[p.ID] {
			if e, ok := u.edits[c.Path]; ok {
				now[j].StartLine, now[j].EndLine = e.Moved(c.StartLine, c.EndLine)
			}
			swap = append(swap, link(baseSHA, c), link(src.sha, now[j]))
		}
		swap = append(swap, "/blob/"+baseSHA+"/", "/blob/"+src.sha+"/") // a link no stored citation names
		pages[i].Content, pages[i].Cites = strings.NewReplacer(swap...).Replace(p.Content), now
		kept++
	}
	if err := g.store(ctx, snap, pages); err != nil {
		return false, err
	}
	progress.Logf("carried %d of %d pages over from %.7s, where %d files differ and %d import a declaration it removed or altered; writing the rest",
		kept, len(pages), baseSHA, len(changed), len(ripple))
	return true, nil
}

// update is what a change did, as carry weighs each page against it.
type update struct {
	changed, ripple map[string]bool            // the files that differ, and those importing a declaration that changed
	edits           map[string]change.Edit     // by changed file a page cites: what became of its lines
	known           map[string]map[string]bool // by changed file base held: the symbols of its chunks there
	groups          map[int][]string           // by group of base's pages (group): the files they cite
}

// weigh gathers what carry weighs base's pages against.
func (g *Generator) weigh(ctx context.Context, base, snap int64, changed, ripple map[string]bool, old []page, cites map[int64][]evidence.Citation) (update, error) {
	u := update{changed: changed, ripple: ripple, edits: map[string]change.Edit{}, groups: map[int][]string{}}
	for _, p := range old {
		for _, c := range cites[p.ID] {
			u.groups[group(p)] = append(u.groups[group(p)], c.Path)
			if _, done := u.edits[c.Path]; done || !changed[c.Path] {
				continue
			}
			before, held, err := g.content(ctx, base, c.Path)
			if err != nil {
				return update{}, err
			}
			after, holds, err := g.content(ctx, snap, c.Path)
			if err != nil {
				return update{}, err
			}
			u.edits[c.Path] = change.Edit{} // removed: none of its lines are left
			if held && holds {
				u.edits[c.Path] = change.Diff(before, after)
			}
		}
	}
	var err error
	u.known, err = g.Store.Symbols(ctx, base, slices.Collect(maps.Keys(changed)))
	return u, err
}

// group is the group of pages p belongs with: its section, or p alone in none.
func group(p page) int {
	if p.Parent >= 0 {
		return p.Parent
	}
	return p.Position
}

// touched reports whether the change may make page p read differently: a
// line it cites is no longer there as it was (for a manifest, only where the
// page says a word the change lost: a new dependency version leaves the pages
// citing its scripts), a file it cites imports a declaration the change
// altered, or its search now finds a declaration that is new: in a changed
// file, or anywhere in a new file that belongs with p's group of pages
// (change.Home), as a new file is for the pages nearest it. The same chunk
// with other lines around it, a declaration now in another chunk
// (change.Novel), or a manifest, it may find without harm.
func (g *Generator) touched(ctx context.Context, snap int64, p page, cites []evidence.Citation, u update) (bool, error) {
	if slices.ContainsFunc(cites, func(c evidence.Citation) bool {
		e, edited := u.edits[c.Path]
		return u.ripple[c.Path] || edited && e.Touches(c.Path, c.StartLine, c.EndLine, p.Content)
	}) {
		return true, nil
	}
	// ponytail: the page's own search only; the sources its section shares count
	// where it cites them, so one changed file does not rewrite a whole section.
	_, hits, err := g.sources(ctx, snap, p, page{}, false)
	return slices.ContainsFunc(hits, func(h retrieval.Hit) bool {
		if !u.changed[h.Path] || change.Is(h.Path) {
			return false
		}
		if symbols, held := u.known[h.Path]; held {
			return change.Novel(h.Symbol, symbols)
		}
		return change.Home(h.Path, u.groups)[group(p)]
	}), err
}

// content reads file p of the snapshot, and whether it holds one.
func (g *Generator) content(ctx context.Context, snap int64, p string) (string, bool, error) {
	f, err := g.Catalog.File(ctx, catalog.Ref{ID: snap}, p)
	if errors.Is(err, catalog.ErrNotFound) {
		return "", false, nil
	}
	return f.Content, err == nil, err
}
