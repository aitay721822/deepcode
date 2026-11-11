package rust

import (
	"cmp"
	"path"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// crate is a Cargo.toml: a package, a workspace root, or both.
type crate struct {
	dir   string   // its Cargo.toml's directory, "" at the repository's root
	names []string // what other crates use it by: the package's name and its [lib] name, - as _
	lib   string   // its library's root file, src/lib.rs unless [lib] path says another
	bins  []string // the root files [[bin]] path names
	deps  []string // the directories its path dependencies name, [workspace.dependencies]' and [patch]'s too
	pkg   bool     // it has a [package]
	space bool     // it has a [workspace]
	// members and exclude are its [workspace]'s, relative to dir.
	members, exclude []string
}

// manifest is what crate reads of a Cargo.toml; keys match fields regardless
// of case.
type manifest struct {
	Package   *struct{ Name string }
	Lib       struct{ Name, Path string }
	Bin       []struct{ Path string }
	Workspace *struct {
		Members, Exclude []string
		Dependencies     map[string]any
	}
	deps
	Target map[string]deps // [target.'cfg(…)'.dependencies] and the like
	Patch  map[string]map[string]any
}

// deps are a manifest's dependency tables, each by a dependency's name: a
// version string, or a table that may hold a path.
type deps struct {
	Dependencies      map[string]any
	DevDependencies   map[string]any `toml:"dev-dependencies"`
	BuildDependencies map[string]any `toml:"build-dependencies"`
}

// readCargo reads the Cargo.toml in dir, src its content.
// ponytail: a [[test]], [[example]] or [[bench]] path and package.workspace
// go unread; read them if a crate needs those.
func readCargo(dir, src string) (crate, error) {
	var m manifest
	if err := toml.Unmarshal([]byte(src), &m); err != nil {
		return crate{}, err
	}
	c := crate{dir: dir, lib: path.Join(dir, cmp.Or(m.Lib.Path, "src/lib.rs")), pkg: m.Package != nil, space: m.Workspace != nil}
	if m.Package != nil {
		c.names = append(c.names, strings.ReplaceAll(m.Package.Name, "-", "_"))
	}
	if m.Lib.Name != "" {
		c.names = append(c.names, strings.ReplaceAll(m.Lib.Name, "-", "_"))
	}
	for _, b := range m.Bin {
		if b.Path != "" {
			c.bins = append(c.bins, path.Join(dir, b.Path))
		}
	}
	tables := []map[string]any{m.Dependencies, m.DevDependencies, m.BuildDependencies}
	if m.Workspace != nil {
		c.members, c.exclude = m.Workspace.Members, m.Workspace.Exclude
		tables = append(tables, m.Workspace.Dependencies)
	}
	for _, t := range m.Target {
		tables = append(tables, t.Dependencies, t.DevDependencies, t.BuildDependencies)
	}
	for _, t := range m.Patch {
		tables = append(tables, t)
	}
	for _, t := range tables {
		for _, v := range t {
			if table, _ := v.(map[string]any); table != nil {
				if p, ok := table["path"].(string); ok {
					c.deps = append(c.deps, path.Join(dir, p))
				}
			}
		}
	}
	return c, nil
}

// member reports whether the package in dir d is one of workspace w's: w's
// own, or matched by a members pattern and under no exclude path.
// ponytail: path.Match, so ** in a members glob matches one directory as *
// does, and a root package's path dependencies are no members unless listed;
// a glob walker if a workspace needs those.
func (w crate) member(d string) bool {
	if d == w.dir {
		return true
	}
	rel := d
	if w.dir != "" {
		rel = strings.TrimPrefix(d, w.dir+"/")
	}
	for _, e := range w.exclude {
		if e = path.Clean(e); rel == e || strings.HasPrefix(rel, e+"/") {
			return false
		}
	}
	for _, m := range w.members {
		if ok, _ := path.Match(path.Clean(m), rel); ok {
			return true
		}
	}
	return false
}

// Lib is the library root file of the package whose Cargo.toml in dir holds
// src, src/lib.rs unless [lib] path says another; false for a workspace root
// with no [package] of its own, or a Cargo.toml that is no TOML.
func Lib(dir, src string) (string, bool) {
	c, _ := readCargo(dir, src)
	return c.lib, c.pkg
}

// rootFiles are the paths, relative to a package's directory, of the crate
// roots Cargo finds on its own besides the library; each declares its child
// modules in its own directory.
var rootFiles = []string{
	"src/main.rs", "build.rs", "src/bin/*.rs", "src/bin/*/main.rs",
	"tests/*.rs", "tests/*/main.rs", "examples/*.rs", "examples/*/main.rs", "benches/*.rs", "benches/*/main.rs",
}

// isRoot reports whether f, a file of c, is one of its crate roots.
func (c crate) isRoot(f string) bool {
	if f == c.lib {
		return true
	}
	for _, b := range c.bins {
		if f == b {
			return true
		}
	}
	rel := f
	if c.dir != "" {
		rel = strings.TrimPrefix(f, c.dir+"/")
	}
	for _, p := range rootFiles {
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
	}
	return false
}
