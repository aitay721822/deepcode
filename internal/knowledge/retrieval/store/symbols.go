package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"unicode"

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

// Reached is, for each (path, line) of the snapshot, the chunks the
// declaration holding the line reaches in its own file, in this order: the
// rest of its lines, its members' (a qualified name its own and "."), then
// those of the file's functions, methods and constructors whose name its own
// lines hold as a word, members and named ones by their first line; each
// chunk once in the list, the one holding the line left out; none for line 0
// or a line no declaration holds. Its own lines are its doc comment's too, so
// a name a comment holds counts, and a common name (New) another package's
// call holds: one more chunk of related code. Calls into other files are left
// to search, as call_edges resolve them by name alone.
// ponytail: no cap on a list (a large class reaches every chunk of it), as
// evidence.Format cuts it to the budget; cap it if reading them shows.
func (s *Store) Reached(ctx context.Context, snapshotID int64, paths []string, lines []int) ([][]int64, error) {
	out := make([][]int64, len(paths))
	type file struct {
		decls  []decl
		chunks []span
	}
	files := map[string]file{}
	for i, p := range paths {
		if lines[i] == 0 {
			continue
		}
		f, ok := files[p]
		if !ok {
			var err error
			if f.decls, err = s.decls(ctx, snapshotID, p); err != nil {
				return nil, err
			}
			if len(f.decls) > 0 {
				if f.chunks, err = s.spans(ctx, snapshotID, p); err != nil {
					return nil, err
				}
			}
			files[p] = f
		}
		out[i] = reach(f.decls, f.chunks, lines[i])
	}
	return out, nil
}

// decl is a symbol of one file; span a chunk of it.
type (
	decl struct {
		name, qualified, kind string
		start, end            int
	}
	span struct {
		id         int64
		start, end int
		content    string
	}
)

// reach is Reached's list for line, from its file's symbols and chunks, both
// by their first line.
func reach(decls []decl, chunks []span, line int) []int64 {
	var d *decl // the innermost: the last to start, then the shortest
	for i, x := range decls {
		if x.start <= line && line <= x.end && (d == nil || x.start > d.start || x.start == d.start && x.end < d.end) {
			d = &decls[i]
		}
	}
	if d == nil {
		return nil
	}
	holding := func(line int) int64 {
		for _, c := range chunks {
			if c.start <= line && line <= c.end {
				return c.id
			}
		}
		return 0
	}
	own := holding(line)
	var ids []int64
	add := func(id int64) {
		if id != 0 && id != own && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	words := map[string]bool{}
	for _, c := range chunks {
		if c.start > d.end || c.end < d.start {
			continue
		}
		add(c.id)
		ls := strings.Split(c.content, "\n") // a chunk's content is its lines, start to end; a fixture's may be fewer
		for n := max(d.start, c.start); n <= min(d.end, c.end) && n-c.start < len(ls); n++ {
			for _, w := range strings.FieldsFunc(ls[n-c.start], func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '$'
			}) {
				words[w] = true
			}
		}
	}
	for _, x := range decls {
		if strings.HasPrefix(x.qualified, d.qualified+".") {
			add(holding(x.start))
		}
	}
	for _, x := range decls {
		if words[x.name] && (x.kind == "function" || x.kind == "method" || x.kind == "constructor") {
			add(holding(x.start))
		}
	}
	return ids
}

// decls reads the symbols of the snapshot's file p, by their first line.
func (s *Store) decls(ctx context.Context, snapshotID int64, p string) ([]decl, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT name, qualified_name, kind, start_line, end_line FROM symbols
		WHERE snapshot_id = ? AND path = ? ORDER BY start_line, id`, snapshotID, p)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []decl
	for rows.Next() {
		var d decl
		if err := rows.Scan(&d.name, &d.qualified, &d.kind, &d.start, &d.end); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// spans reads the chunks of the snapshot's file p, by their first line.
func (s *Store) spans(ctx context.Context, snapshotID int64, p string) ([]span, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, start_line, end_line, content FROM chunks
		WHERE snapshot_id = ? AND path = ? ORDER BY start_line`, snapshotID, p)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []span
	for rows.Next() {
		var c span
		if err := rows.Scan(&c.id, &c.start, &c.end, &c.content); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type SCIPHit struct {
	Hit
	ReferenceLine int
}

func (s *Store) HasSCIP(ctx context.Context, snapshot int64) (bool, error) {
	var indexed bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM scip_occurrences WHERE snapshot_id = ? AND definition_name <> ''
	)`, snapshot).Scan(&indexed)
	return indexed, err
}

// SCIPUsers selects one indexed definition and finds chunks at its references.
// found distinguishes a covered definition with no references from a missing
// or ambiguous definition. Never widen an exact query to same-name symbols.
func (s *Store) SCIPUsers(ctx context.Context, snapshot int64, name, path string, line, limit int) ([]SCIPHit, bool, error) {
	rows, err := s.DB.QueryContext(ctx, `WITH definition AS (
		SELECT min(o.symbol) AS symbol FROM scip_occurrences o
		JOIN snapshots s ON s.id = o.snapshot_id AND s.status = 'ready'
		WHERE o.snapshot_id = ?1 AND o.path = ?2 AND o.line = ?3 AND o.definition_name = ?4 AND o.definition_name <> ''
		HAVING count(DISTINCT o.symbol) = 1
	), refs AS (
		SELECT min(c.id) AS id, r.path, r.line FROM definition d
		JOIN scip_occurrences r ON r.snapshot_id = ?1 AND r.symbol = d.symbol AND r.definition_name = ''
		JOIN chunks c ON c.snapshot_id = r.snapshot_id AND c.path = r.path AND r.line BETWEEN c.start_line AND c.end_line
		GROUP BY r.path, r.line
		ORDER BY r.path, r.line LIMIT ?5
	)
	SELECT refs.id, c.path, c.symbol_name, c.start_line, c.end_line, c.content, refs.line
	FROM definition LEFT JOIN refs ON true LEFT JOIN chunks c ON c.id = refs.id
	ORDER BY refs.path, refs.line`, snapshot, path, line, name, limit)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var hits []SCIPHit
	var found bool
	for rows.Next() {
		var id, start, end, ref sql.NullInt64
		var path, symbol, content sql.NullString
		if err := rows.Scan(&id, &path, &symbol, &start, &end, &content, &ref); err != nil {
			return nil, false, err
		}
		found = true
		if id.Valid {
			hits = append(hits, SCIPHit{
				Hit: Hit{ChunkID: id.Int64, Path: path.String, Symbol: symbol.String,
					StartLine: int(start.Int64), EndLine: int(end.Int64), Content: content.String},
				ReferenceLine: int(ref.Int64),
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return hits, found, nil
}
