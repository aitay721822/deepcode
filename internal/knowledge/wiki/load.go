package wiki

import (
	"context"
	"database/sql"
	"errors"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/repo/catalog"
)

type Page struct {
	Slug      string              `json:"slug"`
	Title     string              `json:"title"`
	Content   string              `json:"content"`
	Citations []evidence.Citation `json:"citations"`
}

type Wiki struct {
	SnapshotID int64  `json:"snapshotId"`
	CommitSHA  string `json:"commitSha"`
	IndexedAt  string `json:"indexedAt"`
	Status     string `json:"status"`
	Pages      []Page `json:"pages"`
}

// Reader reads the wikis that Generator writes.
type Reader struct{ DB *sql.DB }

// Load returns the wiki to show for a repository: the newest indexed
// snapshot with a finished wiki, else the published snapshot's pages so far,
// else the newest other indexed snapshot's pages so far, else an empty wiki
// with status "none". A pruned snapshot is never shown: its files are gone,
// so its citations could not be opened.
func (r Reader) Load(ctx context.Context, repoID int64) (Wiki, error) {
	w := Wiki{Status: catalog.WikiNone, Pages: []Page{}}
	db := r.DB
	err := db.QueryRowContext(ctx, `
		SELECT s.id, s.commit_sha, coalesce(s.indexed_at, ''), s.wiki_status FROM snapshots s
		WHERE s.repository_id = ?1 AND s.status = 'ready' AND EXISTS (SELECT 1 FROM wiki_pages p WHERE p.snapshot_id = s.id)
		ORDER BY s.wiki_status = ?2 DESC, s.id = (SELECT ready_snapshot_id FROM repositories WHERE id = ?1) DESC, s.id DESC
		LIMIT 1`, repoID, catalog.WikiReady).Scan(&w.SnapshotID, &w.CommitSHA, &w.IndexedAt, &w.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return w, nil
	}
	if err != nil {
		return Wiki{}, err
	}
	cites, err := citations(ctx, db, w.SnapshotID)
	if err != nil {
		return Wiki{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, slug, title, content FROM wiki_pages WHERE snapshot_id = ? ORDER BY position`, w.SnapshotID)
	if err != nil {
		return Wiki{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p Page
		if err := rows.Scan(&id, &p.Slug, &p.Title, &p.Content); err != nil {
			return Wiki{}, err
		}
		p.Citations = append([]evidence.Citation{}, cites[id]...) // [] rather than null in JSON
		w.Pages = append(w.Pages, p)
	}
	return w, rows.Err()
}

// citations reads the citations of snapshot snap's wiki, by page id.
func citations(ctx context.Context, db *sql.DB, snap int64) (map[int64][]evidence.Citation, error) {
	rows, err := db.QueryContext(ctx, `
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
