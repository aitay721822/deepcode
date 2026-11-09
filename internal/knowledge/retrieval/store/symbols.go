package store

import (
	"context"
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
	return scanIDs(s.DB.QueryContext(ctx, `SELECT min(c.id) FROM symbols s
		JOIN file_edges e ON e.snapshot_id = s.snapshot_id AND e.dst = s.path AND e.src <> s.path
		JOIN chunks c ON c.snapshot_id = s.snapshot_id AND c.path = e.src AND instr(c.content, s.name) > 0
		WHERE s.snapshot_id = ? AND s.name COLLATE NOCASE IN (`+in+`) AND s.name IN (`+in+`) AND `+secondary+` = 1.0
		GROUP BY c.path ORDER BY count(DISTINCT s.name) DESC, min(c.id) LIMIT ?`, append(args, limit)...))
}
