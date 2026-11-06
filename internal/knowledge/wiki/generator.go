// Package wiki generates a DeepWiki-style set of pages for a snapshot: the
// model plans the pages from the repository layout (or .devin/wiki.json lists
// them), then writes each page from retrieved evidence with checked citations.
// A new snapshot carries over the pages of the previous wiki that its change
// does not touch.
package wiki

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/catalog"
)

type Generator struct {
	DB        *sql.DB
	Catalog   *catalog.Catalog
	Retriever *retrieval.Retriever
	Chat      *ai.OpenAI
	Language  string
}

const (
	maxPages      = 8
	pageHits      = 12
	evidenceChars = 48000

	// A planned page's title and query, in characters: a prompt-injected README
	// could ask for one the size of a page, shown in the nav and the slug.
	maxTitle = 120
	maxQuery = 500
)

// source is where the wiki's citations link: a commit of a repository on its git server.
type source struct {
	url, kind, sha string
}

// page is a stored wiki page; content is empty until it has been written.
type page struct {
	id       int64
	title    string
	query    string
	position int
	content  string
}

// Run generates the wiki of the READY snapshot of repoID at sha. Its pages are
// stored before any is written, so a retry resumes with the pages left.
func (g *Generator) Run(ctx context.Context, repoID int64, sha string, progress jobs.Progress) (err error) {
	r, err := g.Catalog.Get(ctx, repoID)
	if errors.Is(err, catalog.ErrNotFound) {
		return jobs.Permanent(err)
	} else if err != nil {
		return err
	}
	var snap int64
	var status, wikiStatus, language string
	err = g.DB.QueryRowContext(ctx, `SELECT id, status, wiki_status, wiki_language FROM snapshots WHERE repository_id = ? AND commit_sha = ?`,
		repoID, sha).Scan(&snap, &status, &wikiStatus, &language)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.Permanent(fmt.Errorf("snapshot %s is gone", sha))
	} else if err != nil {
		return err
	}
	src := source{url: r.URL, kind: r.Provider, sha: sha}
	if status != "ready" || wikiStatus == catalog.WikiReady { // a re-index republishes a snapshot whose wiki is written
		progress.Logf("snapshot %.7s is %s with a %s wiki: nothing to generate", sha, status, wikiStatus)
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

	if language != g.Language {
		// Pages stored in another language (DEEPCODE_WIKI_LANGUAGE changed since,
		// or they predate wiki_language) are dropped and planned again, so no wiki
		// mixes two languages.
		if _, err := g.DB.ExecContext(ctx, `DELETE FROM wiki_pages WHERE snapshot_id = ?`, snap); err != nil {
			return err
		}
	}
	repo := r.Owner + "/" + r.Name
	steer, err := g.steer(ctx, catalog.Ref{ID: snap, CommitSHA: sha}, progress)
	if err != nil {
		return err
	}
	pages, err := g.plannedPages(ctx, repoID, snap, sha, repo, steer, progress)
	if err != nil {
		return err
	}
	titles := make([]string, len(pages))
	for i, p := range pages {
		titles[i] = p.title
	}
	for i, p := range pages {
		progress.Stage("write", i, len(pages))
		if p.content != "" {
			continue
		}
		progress.Logf("== page %d of %d: %s", i+1, len(pages), p.title)
		content, cites, err := g.write(ctx, snap, repo, src, p, titles, steer.notes, progress)
		if err != nil {
			return err
		}
		if err := g.save(ctx, p.id, content, cites); err != nil {
			return err
		}
		progress.Logf("== saved with %d checked citations", len(cites))
	}
	progress.Stage("write", len(pages), len(pages))
	g.setStatus(ctx, snap, catalog.WikiReady)
	return nil
}

// setStatus records wiki progress on the snapshot; it runs even when ctx was
// cancelled, so a failed run is never left "generating".
func (g *Generator) setStatus(ctx context.Context, snap int64, s string) {
	if _, err := g.DB.ExecContext(context.WithoutCancel(ctx), `UPDATE snapshots SET wiki_status = ? WHERE id = ?`, s, snap); err != nil {
		log.Printf("wiki: status: %v", err)
	}
}

func (g *Generator) pages(ctx context.Context, snap int64) ([]page, error) {
	rows, err := g.DB.QueryContext(ctx, `SELECT id, title, query, position, content FROM wiki_pages WHERE snapshot_id = ? ORDER BY position`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []page
	for rows.Next() {
		var p page
		if err := rows.Scan(&p.id, &p.title, &p.query, &p.position, &p.content); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// save stores a written page and replaces its citations in one transaction.
func (g *Generator) save(ctx context.Context, pageID int64, content string, cites []evidence.Citation) error {
	tx, err := g.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE wiki_pages SET content = ? WHERE id = ?`, content, pageID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM wiki_citations WHERE page_id = ?`, pageID); err != nil {
		return err
	}
	if err := insertCitations(ctx, tx, pageID, cites); err != nil {
		return err
	}
	return tx.Commit()
}

func insertCitations(ctx context.Context, tx *sql.Tx, pageID int64, cites []evidence.Citation) error {
	for _, c := range cites {
		if _, err := tx.ExecContext(ctx, `INSERT INTO wiki_citations(page_id, path, start_line, end_line) VALUES (?, ?, ?, ?)`,
			pageID, c.Path, c.StartLine, c.EndLine); err != nil {
			return err
		}
	}
	return nil
}

// newPage is a page to store for a snapshot: planned, with no content yet, or
// carried over with its content and citations.
type newPage struct {
	title, query, content string
	cites                 []evidence.Citation
}

// store saves the snapshot's pages, in order, and the language they are
// written in, in one transaction: a resumed job must not take half a plan for
// the whole.
func (g *Generator) store(ctx context.Context, snap int64, pages []newPage) error {
	tx, err := g.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, p := range pages {
		var id int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO wiki_pages(snapshot_id, slug, title, query, position, content) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
			snap, slug(i, p.title), p.title, p.query, i, p.content).Scan(&id); err != nil {
			return err
		}
		if err := insertCitations(ctx, tx, id, p.cites); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE snapshots SET wiki_language = ? WHERE id = ?`, g.Language, snap); err != nil {
		return err
	}
	return tx.Commit()
}

// carry starts the snapshot's wiki from the repository's newest finished wiki
// in the same language: the same pages, each keeping its text unless the
// change may touch it, so only the touched pages are written again. It
// reports false, storing nothing, when there is no such wiki, when
// .devin/wiki.json changed (it may list other pages) or when more than half
// of the files changed, which calls for a new plan.
// ponytail: a subsystem added in a small change gets no page of its own until
// the next new plan; compare the layouts if that bites.
func (g *Generator) carry(ctx context.Context, repoID, snap int64, sha string, progress jobs.Progress) (bool, error) {
	var base int64
	var baseSHA string
	err := g.DB.QueryRowContext(ctx, `
		SELECT id, commit_sha FROM snapshots
		WHERE repository_id = ? AND id <> ? AND status = 'ready' AND wiki_status = ? AND wiki_language = ?
		ORDER BY id DESC LIMIT 1`, repoID, snap, catalog.WikiReady, g.Language).Scan(&base, &baseSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	changed, files, err := g.Catalog.Changed(ctx, base, snap)
	if err != nil {
		return false, err
	}
	if changed[steeringFile] || 2*len(changed) > files {
		progress.Logf("%d of %d files changed since %.7s, or %s did: planning anew", len(changed), files, baseSHA, steeringFile)
		return false, nil
	}
	old, err := g.pages(ctx, base)
	if err != nil {
		return false, err
	}
	cites, err := citations(ctx, g.DB, base)
	if err != nil {
		return false, err
	}
	pages := make([]newPage, len(old))
	kept := 0
	for i, p := range old {
		pages[i] = newPage{title: p.title, query: p.query}
		touched, err := g.touched(ctx, snap, p, cites[p.id], changed)
		if err != nil {
			return false, err
		}
		if !touched {
			// The cited files did not change, so their lines and citations hold;
			// the links move to the new commit.
			pages[i].content = strings.ReplaceAll(p.content, "/blob/"+baseSHA+"/", "/blob/"+sha+"/")
			pages[i].cites = cites[p.id]
			kept++
		}
	}
	if err := g.store(ctx, snap, pages); err != nil {
		return false, err
	}
	progress.Logf("carried %d of %d pages over from %.7s, where %d files differ; writing the rest", kept, len(pages), baseSHA, len(changed))
	return true, nil
}

// touched reports whether the change may make page p read differently: a
// file it cites, or one that its search now finds, was added, edited or
// removed.
func (g *Generator) touched(ctx context.Context, snap int64, p page, cites []evidence.Citation, changed map[string]bool) (bool, error) {
	if slices.ContainsFunc(cites, func(c evidence.Citation) bool { return changed[c.Path] }) {
		return true, nil
	}
	hits, err := g.sources(ctx, snap, p)
	return slices.ContainsFunc(hits, func(h retrieval.Hit) bool { return changed[h.Path] }), err
}
