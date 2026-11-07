// Package dotnet turns the facts the analyzer's dotnet package reads from C#
// files, and the project files around them, into edges: a file to each file
// that declares a type it uses, a project file to each project it references.
package dotnet

import (
	"maps"
	"slices"
	"strings"
)

// maxEdges caps the files one source file has edges to.
// ponytail: a file naming more types than 50 (a composition root, a
// DbContext) keeps the first it resolves, in source order; rank them by use
// count if that drops the ones that matter.
const maxEdges = 50

// unit is what one file's facts say.
type unit struct {
	ns, usings, globals, defs []string
	named                     []string // fully qualified: using static X.Y.Z, using A = X.Y.Z
	refs                      []string // as the source spells them
}

// parse sorts a file's facts into a unit.
func parse(facts []string) unit {
	var u unit
	for _, f := range facts {
		kind, v, _ := strings.Cut(f, ":")
		if v == "" {
			continue
		}
		switch kind {
		case "ns":
			u.ns = append(u.ns, v)
		case "using":
			u.usings = append(u.usings, v)
		case "global":
			u.globals = append(u.globals, v)
		case "def":
			u.defs = append(u.defs, v)
		// ponytail: an alias of a namespace (using C = X.Y; C.T) resolves nothing;
		// keep the alias's name in its fact and substitute it if such code loses edges.
		case "static", "alias":
			u.named = append(u.named, v)
		case "type":
			u.refs = append(u.refs, v)
		}
	}
	return u
}

// Resolve turns files, each C# file's facts by repository path, and configs,
// the content of each project file and Directory.Build.props by path, into
// edges {src, dst}: src uses a type dst declares, or src's project references
// dst. A file belongs to the project file in its nearest directory that has
// one, and the global usings of any file of that project, of the project file
// and of its Directory.Build.props apply to all of the project's files.
func Resolve(files map[string][]string, configs map[string]string) [][2]string {
	projs := projects(configs)
	owners := map[string]string{} // directory → its C# or VB.NET project file
	for p := range projs {
		if d := dir(p); hasSources(p) && (owners[d] == "" || p < owners[d]) {
			owners[d] = p
		}
	}
	paths := slices.Sorted(maps.Keys(files)) // so each type's files, and so the capped edges, come in one order
	units := make([]unit, len(paths))
	owner := make([]string, len(paths)) // "" outside any project: those files share their global usings
	defs := map[string][]string{}       // qualified type name → the files declaring it, partial ones several
	globals := map[string][]string{}    // project file → its global usings
	for i, p := range paths {
		units[i] = parse(files[p])
		owner[i], _ = nearest(owners, dir(p))
		globals[owner[i]] = append(globals[owner[i]], units[i].globals...)
		for _, d := range units[i].defs {
			defs[d] = append(defs[d], p)
		}
	}
	for p, pr := range projs {
		globals[p] = append(globals[p], pr.usings...)
	}
	out := references(projs, configs)
	for i, p := range paths {
		out = append(out, uses(p, units[i], scopes(units[i], globals[owner[i]]), defs)...)
	}
	return out
}

// scopes are the namespaces a file's type names resolve in, in C#'s order:
// its own namespaces, innermost first, then their parents, the global
// namespace, then its usings and its project's global usings.
func scopes(u unit, globals []string) []string {
	var out []string
	for _, ns := range slices.Backward(u.ns) {
		for ; ns != ""; ns = parent(ns) {
			out = append(out, ns)
		}
	}
	out = append(append(append(out, ""), u.usings...), globals...)
	seen := map[string]bool{}
	return slices.DeleteFunc(out, func(s string) bool {
		dup := seen[s]
		seen[s] = true
		return dup
	})
}

// uses are the edges from file p: to the types its static usings and
// aliases name, then to the files declaring each type it refers to, up to
// maxEdges files.
func uses(p string, u unit, scopes []string, defs map[string][]string) [][2]string {
	seen := map[string]bool{p: true}
	var out [][2]string
	add := func(dsts []string) {
		for _, d := range dsts {
			if !seen[d] && len(out) < maxEdges {
				seen[d] = true
				out = append(out, [2]string{p, d})
			}
		}
	}
	for _, t := range u.named {
		add(defs[t])
	}
	for _, r := range u.refs {
		add(find(r, scopes, defs))
	}
	return out
}

// find is the files declaring the type name refers to in the first scope
// that has it; failing that, name less its last part (Settings.Instance is a
// property of Settings, Status.Open a member of Status).
func find(name string, scopes []string, defs map[string][]string) []string {
	for ; name != ""; name = parent(name) {
		for _, s := range scopes {
			q := name
			if s != "" {
				q = s + "." + name
			}
			if fs := defs[q]; len(fs) > 0 {
				return fs
			}
		}
	}
	return nil
}

// parent is a dotted name less its last part, "" for a single part.
func parent(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return name[:i]
}
