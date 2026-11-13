// Package wiki generates a DeepWiki-style set of pages for a snapshot: the
// code's structure shapes it (its units and their sections, outline/place),
// the model titles and plans the pages, each request held to its contract,
// then writes each page from retrieved evidence with checked citations,
// Parallel at a time. A new snapshot carries over the pages of the previous
// wiki that its change does not touch.
package wiki

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/knowledge/wiki/change"
	"deepcode/internal/knowledge/wiki/fanout"
	"deepcode/internal/knowledge/wiki/outline"
	"deepcode/internal/knowledge/wiki/outline/place"
	"deepcode/internal/knowledge/wiki/outline/skeleton"
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
	MaxPages  int // the cost ceiling: the most pages a wiki gets, never a target; 0 is defaultMaxPages
	// Planned, when set, is told how each plan the model makes went, Written
	// how each page's writing went, and Touched each page an update carries
	// that is written again, and why (touched's reason, or "moved"). The bench
	// and the probes set them.
	Planned func(Planning)
	Written func(Writing)
	Touched func(page, why string)
}

const (
	defaultMaxPages = 64 // enough for DeepWiki's page counts on the bench's repositories
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
	pages, err := g.plannedPages(ctx, repoID, snap, src, repo, progress)
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
	// The code each guide shows (place.Illustrations), which its heading
	// covers in the files a page lists.
	var home map[string]string
	if slices.ContainsFunc(append(slices.Clone(top), under...), func(p page) bool { return p.Files != nil }) {
		s, err := g.structure(ctx, snap)
		if err != nil {
			return err
		}
		for _, a := range s.asides {
			if a.Home != nil {
				home = a.Home
			}
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
			content, cites, err := g.write(ctx, snap, repo, src, p, pages, home, log)
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

// store saves the snapshot's pages, in order, each keyed by its slug, the
// language they are written in and the wiki's labels, in one transaction: a
// resumed job must not take half a plan for the whole.
func (g *Generator) store(ctx context.Context, snap int64, labels store.Labels, pages []newPage) error {
	for i := range pages {
		pages[i].Slug = slug(i, pages[i].Title)
	}
	return g.Store.Add(ctx, snap, g.Language, labels, pages)
}

// carry starts the snapshot's wiki from the repository's newest finished wiki
// in the same language: the same pages, their files following the change
// (place.Carry: a renamed file keeps its place, a deleted one leaves, a new
// one joins a page of its unit), each keeping its text unless its files
// changed or the change may touch it, so only those pages are written again;
// a kept page's citations follow their lines, and its links go to the new
// commit. It reports false, storing nothing, when there is no such wiki, when
// that wiki was planned before pages held their files, when more than half
// of the files changed, or when a section's worth of code came or went
// (place.Replan).
func (g *Generator) carry(ctx context.Context, repoID, snap int64, src source, progress jobs.Progress) (bool, error) {
	base, baseSHA, err := g.Store.Base(ctx, repoID, snap, g.Language)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	old, err := g.Store.Pages(ctx, base)
	if err != nil {
		return false, err
	}
	if len(old) == 0 { // a ready wiki of no pages: nothing to carry
		return false, nil
	}
	if slices.ContainsFunc(old, func(p page) bool { return p.Files == nil }) {
		progress.Logf("the wiki of %.7s was planned before its pages held their files: planning anew", baseSHA)
		return false, nil
	}
	changed, files, err := g.Catalog.Changed(ctx, base, snap)
	if err != nil {
		return false, err
	}
	if 2*len(changed) > files {
		progress.Logf("%d of %d files changed since %.7s: planning anew", len(changed), files, baseSHA)
		return false, nil
	}
	was, err := g.structure(ctx, base)
	if err != nil {
		return false, err
	}
	is, err := g.structure(ctx, snap)
	if err != nil {
		return false, err
	}
	// The code units as planned, but before the thin merge: what the guides
	// show is the docs'. A wiki planned before the guides showed code keeps
	// its pages of it; a plan anew moves them.
	wasRaw, wasAsides := place.Raw(was.paths, was.lines, was.declared)
	wasRaw, _ = place.Illustrations(wasRaw, wasAsides, was.edges, was.lines)
	isRaw, isAsides := place.Raw(is.paths, is.lines, is.declared)
	isRaw, _ = place.Illustrations(isRaw, isAsides, is.edges, is.lines)
	if why := place.Replan(wasRaw, isRaw, is.edges); why != "" {
		progress.Logf("since %.7s %s: planning anew", baseSHA, why)
		return false, nil
	}
	renamed, deleted, added, err := g.moves(ctx, base, snap, changed, was.paths, is.paths, was.lines)
	if err != nil {
		return false, err
	}
	plan, heads := make([]outline.Page, len(old)), map[int]bool{}
	for i, p := range old {
		plan[i] = outline.Page{Title: p.Title, Chosen: p.Files.Chosen, Placed: p.Files.Placed, Units: p.Files.Units, Parent: p.Parent}
		if p.Parent >= 0 {
			heads[p.Parent] = true
		}
	}
	plan, from, moved := place.Carry(plan, heads, renamed, deleted, added, place.Owners(isRaw, is.asides, is.lines), is.edges)
	// A page may also read differently when a file it cites imports a
	// declaration the change altered.
	ripple, err := g.Graph.Impact(ctx, base, snap, changed)
	if err != nil {
		return false, err
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
	pages := make([]newPage, len(plan))
	kept := 0
	for i, q := range plan {
		p := old[from[i]]
		was, err := g.skeleton(ctx, base, p, heads[p.Position]) // the headings its plan gave it at base
		if err != nil {
			return false, err
		}
		pages[i] = newPage{Title: p.Title, Query: p.Query, Parent: q.Parent, Files: store.PageFiles{Chosen: q.Chosen, Placed: q.Placed, Units: q.Units}}
		p.Files = &pages[i].Files
		head := slices.ContainsFunc(plan, func(q outline.Page) bool { return q.Parent == i })
		why, err := g.touched(ctx, snap, p, head, was, cites[p.ID], u)
		if err != nil {
			return false, err
		}
		if why == "" && moved[i] {
			why = "moved"
		}
		if why != "" {
			if g.Touched != nil {
				g.Touched(p.Title, why)
			}
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
	labels, err := g.Store.Labels(ctx, base)
	if err != nil {
		return false, err
	}
	if err := g.store(ctx, snap, labels, pages); err != nil {
		return false, err
	}
	progress.Logf("carried %d of %d pages over from %.7s, where %d files differ (%d renamed, %d deleted, %d new) and %d import a declaration it removed or altered; writing the rest",
		kept, len(pages), baseSHA, len(changed), len(renamed), len(deleted), len(added), len(ripple))
	return true, nil
}

// moves splits the files that changed between base and snap (was and is
// hold their paths, lines base's line counts): a path gone from base and a
// new one with its content is a rename, unless it was empty, as many
// unrelated files are; the rest gone are deleted, the rest new added, in
// path order.
func (g *Generator) moves(ctx context.Context, base, snap int64, changed map[string]bool, was, is []string, lines map[string]int) (map[string]string, map[string]bool, []string, error) {
	var gone, fresh []string
	for p := range changed {
		_, in := slices.BinarySearch(was, p)
		_, now := slices.BinarySearch(is, p)
		if in && !now {
			gone = append(gone, p)
		} else if now && !in {
			fresh = append(fresh, p)
		}
	}
	sort.Strings(gone)
	sort.Strings(fresh)
	before, err := g.Store.Hashes(ctx, base, gone)
	if err != nil {
		return nil, nil, nil, err
	}
	after, err := g.Store.Hashes(ctx, snap, fresh)
	if err != nil {
		return nil, nil, nil, err
	}
	byHash := map[string]string{}
	for _, p := range fresh {
		if _, taken := byHash[after[p]]; !taken {
			byHash[after[p]] = p
		}
	}
	renamed, deleted, used := map[string]string{}, map[string]bool{}, map[string]bool{}
	for _, p := range gone {
		if n, ok := byHash[before[p]]; ok && !used[n] && lines[p] > 0 {
			renamed[p], used[n] = n, true
		} else {
			deleted[p] = true
		}
	}
	var added []string
	for _, p := range fresh {
		if !used[p] {
			added = append(added, p)
		}
	}
	return renamed, deleted, added, nil
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

// touched is why the change may make page p, a section's head when head,
// read differently, "" for no reason: "cites", a line it cites is no longer
// there as it was (for a manifest, only where the page says a word the change
// lost: a new dependency version leaves the pages citing its scripts), or a
// file it cites imports a declaration the change altered; "search", its
// search, its skeleton's anchors first, now finds a declaration that is new:
// in a changed file, or anywhere in a new file that belongs with p's group of
// pages (change.Home), as a new file is for the pages nearest it; or
// "skeleton", its plan now gives it a heading that its plan at base (was) did
// not, whose source its evidence shows and which its text lacks (a guide's
// new section, a chosen file's renamed type). The same chunk with other lines
// around it, a declaration now in another chunk (change.Novel), a manifest, a
// heading the plan no longer gives, or one it gave before that the text lacks
// (the writer left it out, or wrote the page, from the files it has, before
// skeletons), it may find without harm.
func (g *Generator) touched(ctx context.Context, snap int64, p page, head bool, was skeleton.Skeleton, cites []evidence.Citation, u update) (string, error) {
	if slices.ContainsFunc(cites, func(c evidence.Citation) bool {
		e, edited := u.edits[c.Path]
		return u.ripple[c.Path] || edited && e.Touches(c.Path, c.StartLine, c.EndLine, p.Content)
	}) {
		return "cites", nil
	}
	var sk skeleton.Skeleton
	if p.Position != 0 { // the overview's headings are fixed, in the wiki's language
		var err error
		if sk, err = g.skeleton(ctx, snap, p, head); err != nil {
			return "", err
		}
	}
	// ponytail: the page's own search only, a section's head's too; the sources its
	// section shares count where it cites them, so one changed file does not
	// rewrite a whole section. A gained heading this evidence shows but write's,
	// the section's first, leaves out costs one rewrite: the next base gives it.
	_, hits, err := g.sources(ctx, snap, p, page{}, false, nil, sk.Anchors())
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(hits, func(h retrieval.Hit) bool {
		if !u.changed[h.Path] || change.Is(h.Path) {
			return false
		}
		if symbols, held := u.known[h.Path]; held {
			return change.Novel(h.Symbol, symbols)
		}
		return change.Home(h.Path, u.groups)[group(p)]
	}) {
		return "search", nil
	}
	_, used, _ := evidence.Format(hits, evidence.Limit{Chars: evidenceChars}) // as write holds them to the evidence
	if sk, _ = sourced(sk, used); len(skeleton.Lacking(skeleton.Gained(was, sk), skeleton.Parse(p.Content, sk))) > 0 {
		return "skeleton", nil
	}
	return "", nil
}

// content reads file p of the snapshot, and whether it holds one.
func (g *Generator) content(ctx context.Context, snap int64, p string) (string, bool, error) {
	f, err := g.Catalog.File(ctx, catalog.Ref{ID: snap}, p)
	if errors.Is(err, catalog.ErrNotFound) {
		return "", false, nil
	}
	return f.Content, err == nil, err
}
