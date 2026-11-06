package retrieval

import (
	"context"
	"strings"
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

// Symbols is the symbol search behind the UI: substring match on names,
// exact matches first, then prefixes, then shorter names.
func (r *Retriever) Symbols(ctx context.Context, snapshotID int64, q string, limit int) ([]SymbolHit, error) {
	out := []SymbolHit{}
	q = strings.TrimSpace(q)
	if q == "" {
		return out, nil
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	rows, err := r.DB.QueryContext(ctx, `
		SELECT name, qualified_name, kind, path, start_line, end_line, signature FROM symbols
		WHERE snapshot_id = ?1 AND (name LIKE ?2 ESCAPE '\' OR qualified_name LIKE ?2 ESCAPE '\')
		ORDER BY name = ?3 COLLATE NOCASE DESC, name LIKE ?4 ESCAPE '\' DESC, length(name), name, path
		LIMIT ?5`, snapshotID, "%"+esc+"%", q, esc+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s SymbolHit
		if err := rows.Scan(&s.Name, &s.QualifiedName, &s.Kind, &s.Path, &s.StartLine, &s.EndLine, &s.Signature); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
