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

func (s *Store) SCIPSnapshot(ctx context.Context, repository, commit string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT s.id FROM snapshots s JOIN repositories r ON r.id = s.repository_id
		WHERE r.public_id = ? AND s.commit_sha = ? AND s.status = 'ready'`, repository, commit).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("SCIP manifest does not name a ready snapshot")
	}
	return id, err
}

func (s *Store) SCIPSource(ctx context.Context, snapshot int64, path, hash string) (string, error) {
	var content string
	err := s.DB.QueryRowContext(ctx, `SELECT c.content FROM source_files f JOIN file_contents c ON c.hash = f.content_hash
		WHERE f.snapshot_id = ? AND f.path = ? AND f.content_hash = ? AND f.language = 'go'`, snapshot, path, hash).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("SCIP document %q does not match a Go blob in the snapshot", path)
	}
	return content, err
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
	for _, row := range rows {
		if _, err := insert.ExecContext(ctx, snapshot, row.Path, row.Line, row.ByteColumn, row.Symbol, row.DefinitionName); err != nil {
			return err
		}
	}
	return tx.Commit()
}
