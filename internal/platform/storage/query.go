package storage

import (
	"context"
	"database/sql"
	"strings"
)

// Placeholders returns "?,?,...,?" with n markers for IN clauses.
func Placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// RowID is the row id of table's row whose public id, the CUID2 the API, the
// URLs and the UI show (migrations 010, 024), is publicID; sql.ErrNoRows for
// none. table is code's own, never the client's.
func RowID(ctx context.Context, db *sql.DB, table, publicID string) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `SELECT id FROM `+table+` WHERE public_id = ?`, publicID).Scan(&id)
	return id, err
}
