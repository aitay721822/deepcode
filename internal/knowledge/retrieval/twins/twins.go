// Package twins finds a type's twins: the same type declared again in other
// projects, as eShop declares OrderPaymentSucceededIntegrationEvent in
// Ordering.API, which handles it, and in PaymentProcessor, which publishes it.
// A message a bus carries between two projects is two types to the import
// graph; knowing them for one lets the code using either be read as about the
// same thing.
package twins

import (
	"context"
	"path"
	"slices"
	"strings"

	"deepcode/internal/knowledge/retrieval/store"
	"deepcode/internal/repo/index/analyzer"
)

// Twin is one declaration of a type that has twins: its file, its project
// (the folder of the nearest project file) and the base type they share.
type Twin struct{ Path, Project, Base string }

// Of returns the declarations of name that are twins, or none: those of a
// top-level class, record, struct or interface named name in two projects or
// more, tests, examples and docs aside, whose first lines name the same base
// type, a base the snapshot declares once (tests and the like aside again),
// as a class or record. The bounds
// keep out the same name for different things: per-project interfaces,
// nested helper classes, a base each project declares for itself, Rust's and
// Go's types, which have no base.
// ponytail: three queries a call, as users asks about one name; a table
// linked once per snapshot comes with near() and the topology, which want
// every name's.
func Of(ctx context.Context, s *store.Store, snapshotID int64, name string) ([]Twin, error) {
	decls, err := s.Types(ctx, snapshotID, name)
	if err != nil {
		return nil, err
	}
	if decls = primary(decls); len(decls) < 2 {
		return nil, nil
	}
	files, err := s.Projects(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	return among(decls, files, func(b string) ([]store.Type, error) {
		declared, err := s.Types(ctx, snapshotID, b)
		return primary(declared), err
	})
}

// Names returns the names of the snapshot's types that have twins (Of), as a
// set: the message types. It asks once for every name declared more than
// once, then once for each base those derive from.
// ponytail: asked once per gathering; keep it per snapshot if its time shows
// in a gathering's.
func Names(ctx context.Context, s *store.Store, snapshotID int64) (map[string]bool, error) {
	types, err := s.RepeatedTypes(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	files, err := s.Projects(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	byName := map[string][]store.Type{}
	for _, t := range primary(types) {
		byName[t.Name] = append(byName[t.Name], t)
	}
	bases := map[string][]store.Type{} // each base's primary declarations, asked once
	out := map[string]bool{}
	for name, decls := range byName {
		tw, err := among(decls, files, func(b string) ([]store.Type, error) {
			if declared, ok := bases[b]; ok {
				return declared, nil
			}
			declared, err := s.Types(ctx, snapshotID, b)
			bases[b] = primary(declared)
			return bases[b], err
		})
		if err != nil {
			return nil, err
		}
		if len(tw) > 0 {
			out[name] = true
		}
	}
	return out, nil
}

// among returns the twins among decls, the primary declarations of one name:
// the declarations deriving from the first base, in their order, that decls
// of two projects or more derive from, when declared(base), the base's
// primary declarations, is one class or record. files are the snapshot's
// project files.
func among(decls []store.Type, files []string, declared func(base string) ([]store.Type, error)) ([]Twin, error) {
	var bases []string
	byBase := map[string][]Twin{}
	for _, d := range decls {
		for _, b := range Bases(d.Signature) {
			if byBase[b] == nil {
				bases = append(bases, b)
			}
			byBase[b] = append(byBase[b], Twin{Path: d.Path, Project: project(d.Path, files), Base: b})
		}
	}
	for _, b := range bases {
		projects := map[string]bool{}
		for _, t := range byBase[b] {
			projects[t.Project] = true
		}
		if len(projects) < 2 {
			continue
		}
		d, err := declared(b)
		if err != nil {
			return nil, err
		}
		if len(d) == 1 && (d[0].Kind == "class" || d[0].Kind == "record") {
			return byBase[b], nil
		}
	}
	return nil, nil
}

// primary are types but those of tests, examples and docs (analyzer.Secondary).
func primary(types []store.Type) []store.Type {
	return slices.DeleteFunc(types, func(t store.Type) bool { return analyzer.Secondary(t.Path) })
}

// project is the folder of the project file nearest p, of files: "" for the
// root's, or for none.
func project(p string, files []string) string {
	best := ""
	for _, f := range files {
		if dir := path.Dir(f); dir != "." && strings.HasPrefix(p, dir+"/") && len(dir) > len(best) {
			best = dir
		}
	}
	return best
}

// Bases are the types a declaration's first line derives from, each by its
// last name, without type arguments: what follows C#'s ":" (after a record's
// parameters, before a where clause) or Java's extends. Bracketed parts, attributes, parameters and
// type arguments, are read past whole, so a type parameter's bound
// (<T extends Base>) is none.
// ponytail: the first line only, where the analyzer keeps a signature; a
// base on the next line is not seen.
func Bases(signature string) []string {
	var flat strings.Builder
	depth := 0
	for _, r := range signature {
		switch r {
		case '(', '<', '[':
			depth++
		case ')', '>', ']':
			depth = max(depth-1, 0)
		default:
			if depth == 0 {
				flat.WriteRune(r)
			}
		}
	}
	line, _, _ := strings.Cut(flat.String(), " where ") // C#'s constraints: a ":" in them is a bound
	list := ""
	if _, after, ok := strings.Cut(line, " extends "); ok {
		list, _, _ = strings.Cut(after, " implements ")
	} else if _, after, ok := strings.Cut(line, ":"); ok {
		list = after
	}
	list, _, _ = strings.Cut(list, "{")
	list, _, _ = strings.Cut(list, ";")
	var out []string
	for _, b := range strings.Split(list, ",") {
		b = strings.TrimSpace(b)
		b = b[strings.LastIndex(b, ".")+1:]
		if b != "" && !strings.ContainsFunc(b, func(r rune) bool {
			return r != '_' && !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9')
		}) {
			out = append(out, b)
		}
	}
	return out
}
