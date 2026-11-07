// Package store keeps the wikis in SQLite: each snapshot's pages, their
// citations and the language they are written in, and reads what planning
// weighs of a snapshot's files.
package store

import (
	"context"
	"database/sql"
	"errors"

	"deepcode/internal/knowledge/evidence"
)

// Store runs the wikis' SQL.
type Store struct{ DB *sql.DB }

// ErrNotFound: no row matched.
var ErrNotFound = errors.New("找不到")

// Page is a stored wiki page; Content is empty until it has been written.
type Page struct {
	ID       int64
	Slug     string
	Title    string
	Query    string
	Position int
	Parent   int // the position of the section page it is under, or -1 at the top
	Content  string
}

// NewPage is a page to store for a snapshot: planned, with no content yet, or
// carried over with its content and citations. Parent is the index, among
// the pages stored with it, of the section page it is under, or -1.
type NewPage struct {
	Slug, Title, Query, Content string
	Parent                      int
	Cites                       []evidence.Citation
}

// Pages returns the snapshot's pages in order.
func (s *Store) Pages(ctx context.Context, snap int64) ([]Page, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, slug, title, query, position, coalesce(parent, -1), content FROM wiki_pages
		WHERE snapshot_id = ? ORDER BY position`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		var p Page
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Query, &p.Position, &p.Parent, &p.Content); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Add stores the snapshot's pages, in order, and the language they are
// written in, in one transaction.
func (s *Store) Add(ctx context.Context, snap int64, language string, pages []NewPage) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, p := range pages {
		var id int64
		parent := sql.NullInt64{Int64: int64(p.Parent), Valid: p.Parent >= 0 && p.Parent < i}
		if err := tx.QueryRowContext(ctx, `INSERT INTO wiki_pages(snapshot_id, slug, title, query, position, parent, content) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			snap, p.Slug, p.Title, p.Query, i, parent, p.Content).Scan(&id); err != nil {
			return err
		}
		if err := insertCitations(ctx, tx, id, p.Cites); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE snapshots SET wiki_language = ? WHERE id = ?`, language, snap); err != nil {
		return err
	}
	return tx.Commit()
}

// Drop deletes the snapshot's pages.
func (s *Store) Drop(ctx context.Context, snap int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM wiki_pages WHERE snapshot_id = ?`, snap)
	return err
}

// Save stores a written page and replaces its citations in one transaction.
func (s *Store) Save(ctx context.Context, pageID int64, content string, cites []evidence.Citation) error {
	tx, err := s.DB.BeginTx(ctx, nil)
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

// Citations returns the citations of the snapshot's wiki, by page id.
func (s *Store) Citations(ctx context.Context, snap int64) (map[int64][]evidence.Citation, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.page_id, c.path, c.start_line, c.end_line FROM wiki_citations c JOIN wiki_pages p ON p.id = c.page_id
		WHERE p.snapshot_id = ? ORDER BY c.path, c.start_line`, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]evidence.Citation{}
	for rows.Next() {
		var id int64
		var c evidence.Citation
		if err := rows.Scan(&id, &c.Path, &c.StartLine, &c.EndLine); err != nil {
			return nil, err
		}
		out[id] = append(out[id], c)
	}
	return out, rows.Err()
}
