package store

import (
	"context"
	"database/sql"
	"strings"

	"deepcode/internal/platform/storage"
)

type SymbolHit struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualifiedName"`
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	StartLine     int    `json:"startLine"`
	EndLine       int    `json:"endLine"`
	Signature     string `json:"signature"`
}

// Symbols returns up to limit of the snapshot's symbols whose name or
// qualified name holds q (% and _ only as themselves): exact matches first,
// then prefixes, then shorter names. A guide's section is no definition: its
// title is prose.
func (s *Store) Symbols(ctx context.Context, snapshotID int64, q string, limit int) ([]SymbolHit, error) {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	rows, err := s.DB.QueryContext(ctx, `
		SELECT name, qualified_name, kind, path, start_line, end_line, signature FROM symbols
		WHERE snapshot_id = ?1 AND kind <> 'section' AND (name LIKE ?2 ESCAPE '\' OR qualified_name LIKE ?2 ESCAPE '\')
		ORDER BY name = ?3 COLLATE NOCASE DESC, name LIKE ?4 ESCAPE '\' DESC, length(name), name, path
		LIMIT ?5`, snapshotID, "%"+esc+"%", q, esc+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SymbolHit{}
	for rows.Next() {
		var h SymbolHit
		if err := rows.Scan(&h.Name, &h.QualifiedName, &h.Kind, &h.Path, &h.StartLine, &h.EndLine, &h.Signature); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Type is a type's declaration: its name, its file, its kind and its first line.
type Type struct{ Name, Path, Kind, Signature string }

// topTypes selects the snapshot's (?1) top-level declarations of classes,
// records, structs and interfaces: none within another type of its file.
const topTypes = `SELECT name, path, kind, signature FROM symbols s
	WHERE snapshot_id = ?1 AND kind IN ('class', 'record', 'struct', 'interface')
	AND NOT EXISTS (SELECT 1 FROM symbols o WHERE o.snapshot_id = s.snapshot_id AND o.path = s.path AND o.id <> s.id
		AND o.kind IN ('class', 'record', 'struct', 'interface', 'enum') AND o.start_line <= s.start_line AND o.end_line >= s.end_line)`

// Types returns the snapshot's top-level declarations of the classes,
// records, structs and interfaces named name, exactly: none within another
// type of its file.
func (s *Store) Types(ctx context.Context, snapshotID int64, name string) ([]Type, error) {
	return scanTypes(s.DB.QueryContext(ctx, topTypes+` AND name COLLATE NOCASE = ?2 AND name = ?2 ORDER BY path`, snapshotID, name))
}

// RepeatedTypes returns the top-level declarations (Types) of every name that
// more than one of the snapshot's classes, records, structs and interfaces
// declares, by name, then path.
func (s *Store) RepeatedTypes(ctx context.Context, snapshotID int64) ([]Type, error) {
	return scanTypes(s.DB.QueryContext(ctx, topTypes+` AND name IN (SELECT name FROM symbols WHERE snapshot_id = ?1
		AND kind IN ('class', 'record', 'struct', 'interface') GROUP BY name HAVING count(*) > 1) ORDER BY name, path`, snapshotID))
}

func scanTypes(rows *sql.Rows, err error) ([]Type, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Type
	for rows.Next() {
		var t Type
		if err := rows.Scan(&t.Name, &t.Path, &t.Kind, &t.Signature); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Projects returns the paths of the snapshot's project files: .NET's,
// Maven's and Gradle's.
func (s *Store) Projects(ctx context.Context, snapshotID int64) ([]string, error) {
	return scanPaths(s.DB.QueryContext(ctx, `SELECT path FROM source_files WHERE snapshot_id = ? AND (lower(path) GLOB '*.csproj'
		OR lower(path) GLOB '*.vbproj' OR lower(path) GLOB '*.fsproj' OR lower(path) IN ('pom.xml', 'build.gradle', 'build.gradle.kts')
		OR lower(path) GLOB '*/pom.xml' OR lower(path) GLOB '*/build.gradle' OR lower(path) GLOB '*/build.gradle.kts')`, snapshotID))
}

// Users returns the ids of chunks of the files that import a file defining
// one of names and use that name, one chunk a file, the files using most of
// names first, tests, docs and generated files aside; at most limit. Names
// match exactly, case and all.
func (s *Store) Users(ctx context.Context, snapshotID int64, names []string, limit int) ([]int64, error) {
	in := storage.Placeholders(len(names))
	args := []any{snapshotID}
	for range 2 {
		for _, n := range names {
			args = append(args, n)
		}
	}
	ids, err := scanIDs(s.DB.QueryContext(ctx, `SELECT min(c.id) FROM symbols s
		JOIN file_edges e ON e.snapshot_id = s.snapshot_id AND e.dst = s.path AND e.src <> s.path
		JOIN chunks c ON c.snapshot_id = s.snapshot_id AND c.path = e.src AND instr(c.content, s.name) > 0
		WHERE s.snapshot_id = ? AND s.name COLLATE NOCASE IN (`+in+`) AND s.name IN (`+in+`) AND `+secondary+` = 1.0
		GROUP BY c.path ORDER BY count(DISTINCT s.name) DESC, min(c.id) LIMIT ?`, append(args, limit)...))
	if err != nil || len(ids) >= limit {
		return ids, err
	}
	// A caller that names the declaration without importing its file.
	calls, err := scanIDs(s.DB.QueryContext(ctx, `SELECT min(c.id) FROM symbols s
		JOIN call_edges e ON e.snapshot_id = s.snapshot_id AND e.dst = s.path AND e.src <> s.path AND e.name = s.name
		JOIN chunks c ON c.snapshot_id = s.snapshot_id AND c.path = e.src AND instr(c.content, s.name) > 0
		WHERE s.snapshot_id = ? AND s.name COLLATE NOCASE IN (`+in+`) AND s.name IN (`+in+`) AND `+secondary+` = 1.0
		GROUP BY c.path ORDER BY count(DISTINCT s.name) DESC, min(c.id) LIMIT ?`, append(args, limit)...))
	if err != nil {
		return ids, err
	}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	for _, id := range calls {
		if !seen[id] && len(ids) < limit {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
