// Package store joins what importing a repository writes in one
// transaction: the catalog's new repository, and the importer's roles it
// joins.
package store

import (
	"context"
	"database/sql"

	rolestore "deepcode/internal/access/role/store"
)

// JoinRoles is the catalog's onAdd that adds the new repository to roles in
// the transaction that adds it.
func JoinRoles(ctx context.Context, roles []int64) func(tx *sql.Tx, id int64) error {
	return func(tx *sql.Tx, id int64) error { return rolestore.Grant(ctx, tx, roles, id) }
}
