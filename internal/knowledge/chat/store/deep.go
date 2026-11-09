package store

import (
	"context"

	"deepcode/internal/platform/storage"
)

// Defined returns those of names that the snapshot defines as symbols, as the
// code spells them, case and all (the query folds case only to use the index).
// A guide's section is no definition: its title is prose.
func (s *Store) Defined(ctx context.Context, snapshotID int64, names []string) (map[string]bool, error) {
	args := []any{snapshotID}
	for _, n := range names {
		args = append(args, n)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT name FROM symbols WHERE snapshot_id = ? AND kind <> 'section' AND name COLLATE NOCASE IN (`+
		storage.Placeholders(len(names))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	defined := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		defined[n] = true
	}
	return defined, rows.Err()
}

// WikiPages returns the titles and contents of the written wiki pages, in
// order, of snapshot snapshotID, else of the newest of repository repoID's
// indexed snapshots that has written ones.
func (s *Store) WikiPages(ctx context.Context, repoID, snapshotID int64) (titles, contents []string, err error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT title, content FROM wiki_pages WHERE content <> '' AND snapshot_id = (
		SELECT s.id FROM snapshots s WHERE s.repository_id = ? AND s.status = 'ready'
			AND EXISTS (SELECT 1 FROM wiki_pages p WHERE p.snapshot_id = s.id AND p.content <> '')
		ORDER BY s.id = ? DESC, s.id DESC LIMIT 1) ORDER BY position`, repoID, snapshotID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			return nil, nil, err
		}
		titles, contents = append(titles, t), append(contents, c)
	}
	return titles, contents, rows.Err()
}
