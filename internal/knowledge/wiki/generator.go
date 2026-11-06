// Package wiki generates a DeepWiki-style set of pages for a snapshot: the
// model plans the pages from the repository layout, then writes each page from
// retrieved evidence with checked citations.
package wiki

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

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

// Run generates the wiki of the READY snapshot of repoID at sha. Planned pages
// are stored before any is written, so a retry resumes with the pages left.
func (g *Generator) Run(ctx context.Context, repoID int64, sha string, progress jobs.Progress) (err error) {
	r, err := g.Catalog.Get(ctx, repoID)
	if errors.Is(err, catalog.ErrNotFound) {
		return jobs.Permanent(err)
	} else if err != nil {
		return err
	}
	var snap int64
	var status, wikiStatus string
	err = g.DB.QueryRowContext(ctx, `SELECT id, status, wiki_status FROM snapshots WHERE repository_id = ? AND commit_sha = ?`,
		repoID, sha).Scan(&snap, &status, &wikiStatus)
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

	repo := r.Owner + "/" + r.Name
	pages, err := g.plannedPages(ctx, snap, repo, progress)
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
		content, cites, err := g.write(ctx, snap, repo, src, p, titles, progress)
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
	for _, c := range cites {
		if _, err := tx.ExecContext(ctx, `INSERT INTO wiki_citations(page_id, path, start_line, end_line) VALUES (?, ?, ?, ?)`,
			pageID, c.Path, c.StartLine, c.EndLine); err != nil {
			return err
		}
	}
	return tx.Commit()
}
