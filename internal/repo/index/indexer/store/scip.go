package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type SCIPOccurrence struct {
	Path, Symbol, DefinitionName string
	Line, ByteColumn             int
}

func (s *Store) SCIPSnapshot(ctx context.Context, repository, commit string) (snapshot, repoID int64, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT s.id, r.id FROM snapshots s JOIN repositories r ON r.id = s.repository_id
		WHERE r.public_id = ? AND s.commit_sha = ? AND s.status = 'ready'`, repository, commit).Scan(&snapshot, &repoID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, errors.New("SCIP manifest does not name a ready snapshot")
	}
	return snapshot, repoID, err
}

func (s *Store) SCIPSource(ctx context.Context, snapshot int64, path, hash string) (content string, indexed bool, err error) {
	var blob, lang string
	err = s.DB.QueryRowContext(ctx, `SELECT c.content, f.content_hash, f.language FROM source_files f
		JOIN file_contents c ON c.hash = f.content_hash WHERE f.snapshot_id = ? AND f.path = ?`, snapshot, path).Scan(&content, &blob, &lang)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	case blob != hash || lang != "go":
		return "", false, fmt.Errorf("SCIP document %q does not match a Go blob in the snapshot", path)
	}
	return content, true, nil
}

// ReplaceSCIP publishes the entire index or keeps the previous one.
func (s *Store) ReplaceSCIP(ctx context.Context, snapshot int64, rows []SCIPOccurrence) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM snapshots WHERE id = ?`, snapshot).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) || err == nil && status != "ready" {
		return errors.New("snapshot is no longer ready for SCIP import")
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scip_occurrences WHERE snapshot_id = ?`, snapshot); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO scip_occurrences(snapshot_id, path, line, byte_column, symbol, definition_name)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	checked := make(map[string]bool)
	for _, row := range rows {
		// A rebuild can drop a file after validation. Never publish orphaned occurrences.
		if !checked[row.Path] {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM source_files WHERE snapshot_id = ? AND path = ?)`,
				snapshot, row.Path).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("SCIP document %q is no longer indexed", row.Path)
			}
			checked[row.Path] = true
		}
		if _, err := insert.ExecContext(ctx, snapshot, row.Path, row.Line, row.ByteColumn, row.Symbol, row.DefinitionName); err != nil {
			return err
		}
	}
	return tx.Commit()
}
