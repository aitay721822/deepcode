package store

import "context"

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
