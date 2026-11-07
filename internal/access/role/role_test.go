package role

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/access/role/store"
	"deepcode/internal/platform/storage"
)

func setup(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "roles.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []int64{1, 2} {
		db.Exec(`INSERT INTO repositories(id, public_id, url, owner, name) VALUES (?, ?, ?, 'o', 'r')`, id, fmt.Sprintf("r%d", id), fmt.Sprintf("https://github.com/o/r%d", id))
	}
	return &Service{Store: &store.Store{DB: db}}, db
}

// user adds a user holding roles and returns its id.
func user(t *testing.T, db *sql.DB, name string, roles ...int64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO users(username, password_hash) VALUES (?, 'x') RETURNING id`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		if _, err := db.Exec(`INSERT INTO user_roles(user_id, role_id) VALUES (?, ?)`, id, r); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func str(s string) *string                { return &s }
func ids(v ...string) *[]string           { return &v }
func perms(v ...Permission) *[]Permission { return &v }

// TestGrants: a role grants its permissions on its own repositories, roles
// are never merged, and the admin role covers everything.
func TestGrants(t *testing.T) {
	st, db := setup(t)
	ctx := context.Background()
	importers, _ := st.Create(ctx, Input{Name: str("importers"), Permissions: perms(RepoRead, RepoImport), RepositoryIDs: ids("r1")})
	chatters, _ := st.Create(ctx, Input{Name: str("chat on 2"), Permissions: perms(RepoChat), RepositoryIDs: ids("r2")})
	dev := user(t, db, "dev", importers.ID, chatters.ID)
	if r, _ := st.Get(ctx, importers.ID); r.UserCount != 1 {
		t.Fatalf("importers counts %d users, want 1", r.UserCount)
	}
	g, err := st.GrantsOf(ctx, dev)
	switch {
	case err != nil:
		t.Fatal(err)
	case !g.CanOn(RepoRead, 1) || g.CanOn(RepoRead, 2):
		t.Fatalf("read is not scoped to the role's repositories: %+v", g)
	case !g.CanOn(RepoChat, 2) || !g.Can(RepoChat) || g.CanOn(RepoChat, 1):
		t.Fatalf("roles were merged: chat on 2 reached 1: %+v", g)
	case g.Can(UsersManage) || g.AllRepositories():
		t.Fatalf("dev holds more than its roles: %+v", g)
	}
	if got := g.ImportRoles(); len(got) != 1 || got[0] != importers.ID {
		t.Fatalf("import roles: %v", got)
	}

	// A repository granted in a transaction is there once it commits.
	tx, _ := db.Begin()
	if err := store.Grant(ctx, tx, g.ImportRoles(), 2); err != nil || tx.Commit() != nil {
		t.Fatalf("grant: %v", err)
	}
	if g, _ = st.GrantsOf(ctx, dev); !g.CanOn(RepoRead, 2) {
		t.Fatal("the granted repository is not readable")
	}

	admin, _ := st.GrantsOf(ctx, user(t, db, "root", Admin))
	if !admin.AllRepositories() || !admin.CanOn(RepoSync, 2) || len(admin.Permissions()) != len(Permissions) || admin.ImportRoles() != nil {
		t.Fatalf("admin: %+v", admin)
	}
	syncAll, _ := st.Create(ctx, Input{Name: str("sync everything"), Permissions: perms(RepoSync), AllRepositories: new(true)})
	if g, _ := st.GrantsOf(ctx, user(t, db, "syncer", syncAll.ID)); g.AllRepositories() || !g.CanOn(RepoSync, 2) {
		t.Fatalf("every repository without repo.read: %+v", g)
	}
	if none, _ := st.GrantsOf(ctx, user(t, db, "nobody")); none.Can(RepoRead) || len(none.Permissions()) != 0 {
		t.Fatalf("a user without roles: %+v", none)
	}
}

// TestRules: built-in roles keep their names and the admin role its grants;
// names are unique in any case; permissions and repositories must exist.
func TestRules(t *testing.T) {
	st, _ := setup(t)
	ctx := context.Background()
	if _, err := st.Update(ctx, Admin, Input{Permissions: perms(RepoRead)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("narrowed the admin role: %v", err)
	}
	for _, in := range []Input{{AllRepositories: new(false)}, {RepositoryIDs: ids("r1")}} {
		if _, err := st.Update(ctx, Admin, in); !errors.Is(err, ErrConflict) {
			t.Fatalf("narrowed the admin role to some repositories: %v", err)
		}
	}
	if _, err := st.Update(ctx, 2, Input{Name: str("members")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("renamed a built-in role: %v", err)
	}
	all := []Permission{}
	for _, p := range Permissions {
		all = append(all, p.Name)
	}
	if _, err := st.Update(ctx, Admin, Input{Name: str("admin"), Description: str("Everything"), Permissions: &all, AllRepositories: new(true)}); err != nil {
		t.Fatalf("the console sent the admin role back unchanged: %v", err)
	}
	if err := st.Delete(ctx, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted a built-in role: %v", err)
	}
	r, err := st.Create(ctx, Input{Name: str("readers"), Permissions: perms(RepoRead)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(ctx, Input{Name: str("Readers")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	for _, missing := range []string{"r99", "1"} { // a row id names no repository
		if _, err := st.Update(ctx, r.ID, Input{RepositoryIDs: ids(missing)}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("granted the missing repository %q: %v", missing, err)
		}
	}
	if _, err := st.Update(ctx, r.ID, Input{Permissions: perms("repo.everything")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown permission: %v", err)
	}
	if _, err := st.Update(ctx, 99, Input{Name: str("ghosts")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("updated a missing role: %v", err)
	}
	if r, err = st.Update(ctx, r.ID, Input{Description: str("read only"), RepositoryIDs: ids("r2", "r1")}); err != nil ||
		r.Description != "read only" || len(r.Permissions) != 1 || len(r.RepositoryIDs) != 2 {
		t.Fatalf("update kept what it was not given: %+v %v", r, err)
	}
	if r, err = st.Update(ctx, r.ID, Input{Permissions: perms(RepoChat), RepositoryIDs: ids("r1")}); err != nil ||
		!slices.Equal(r.Permissions, []Permission{RepoChat}) || !slices.Equal(r.RepositoryIDs, []string{"r1"}) {
		t.Fatalf("an update added to the role's grants instead of replacing them: %+v %v", r, err)
	}
	if err := st.Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(ctx, Input{Name: str(strings.Repeat("長", 64)), Description: str(strings.Repeat("長", 500))}); err != nil {
		t.Fatalf("64 and 500 characters, three bytes each: %v", err)
	}
}
