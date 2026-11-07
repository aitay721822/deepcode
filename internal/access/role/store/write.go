package store

import (
	"context"
	"database/sql"
	"errors"
)

// Create adds r with the permissions and repositories (public ids) that perms
// and repos set, and returns its id.
func (s *Store) Create(ctx context.Context, r Role, perms *[]Permission, repos *[]string) (int64, error) {
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `INSERT INTO roles(name, description, all_repositories) VALUES (?, ?, ?) RETURNING id`,
			r.Name, r.Description, r.AllRepositories).Scan(&r.ID); err != nil {
			return err
		}
		return links(ctx, tx, r, perms, repos)
	})
	return r.ID, err
}

// Update changes role id in one transaction: edit checks and changes the role
// as stored, and its error ends the transaction. The transaction holds the
// write lock (BEGIN IMMEDIATE) from its start, so no other edit lands between
// the read, made on another connection of the pool, and the write.
func (s *Store) Update(ctx context.Context, id int64, edit func(*Role) error, perms *[]Permission, repos *[]string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		r, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := edit(&r); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE roles SET name = ?, description = ?, all_repositories = ? WHERE id = ?`,
			r.Name, r.Description, r.AllRepositories, id); err != nil {
			return err
		}
		return links(ctx, tx, r, perms, repos)
	})
}

// Delete removes role id.
func (s *Store) Delete(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, id)
	return err
}

// links replaces the permissions and repositories of r that perms and repos
// set; a role on every repository lists none.
func links(ctx context.Context, tx *sql.Tx, r Role, perms *[]Permission, repos *[]string) error {
	if perms != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, r.ID); err != nil {
			return err
		}
		for _, p := range *perms {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO role_permissions(role_id, permission) VALUES (?, ?)`, r.ID, p); err != nil {
				return err
			}
		}
	}
	if repos == nil && !r.AllRepositories {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_repositories WHERE role_id = ?`, r.ID); err != nil {
		return err
	}
	if r.AllRepositories {
		return nil
	}
	for _, repo := range *repos {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM repositories WHERE public_id = ?`, repo).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoRepository
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO role_repositories(role_id, repository_id) VALUES (?, ?)`, r.ID, id)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Grant adds repository repoID to roles within tx, so that a new repository
// joins its importer's roles in the transaction that adds it.
func Grant(ctx context.Context, tx *sql.Tx, roles []int64, repoID int64) error {
	for _, id := range roles {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO role_repositories(role_id, repository_id) VALUES (?, ?)`, id, repoID); err != nil {
			return err
		}
	}
	return nil
}
