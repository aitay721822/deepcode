// Package rust turns the facts the analyzer reads from Rust files into edges
// between them: the modules a file declares and the paths it uses.
package rust

import (
	"path"
	"slices"
	"strings"
)

// Resolve turns Rust files' facts (files maps each repository path to them)
// into edges {src, dst}: src uses dst. configs maps the path of every
// Cargo.toml of the snapshot to its content; other keys are ignored. `mod a;`
// names a.rs or a/mod.rs; crate::, self:: and super:: paths, and those
// starting at a child module or a workspace crate's name, name the file of
// the longest module prefix of the path, along the crate's module tree.
// Order and duplicates are the caller's to settle.
// ponytail: #[path] attributes and renamed dependencies (alias = { package =
// "x" }) go unread, and a plain path inside an inline module reads from the
// file's module; external crates stay unresolved, as they should.
func Resolve(files map[string][]string, configs map[string]string) [][2]string {
	t := build(files, configs)
	var out [][2]string
	for src, facts := range files {
		for _, f := range facts {
			dst := ""
			if name, ok := strings.CutPrefix(f, "mod:"); ok {
				dst = t.modFile(src, name)
			} else if p, ok := strings.CutPrefix(f, "use:"); ok {
				dst = t.target(src, strings.Split(p, "::"))
			}
			if dst != "" && dst != src {
				out = append(out, [2]string{src, dst})
			}
		}
	}
	return out
}

// module is a file's place in a crate: its root file and its module path.
type module struct {
	root string
	path []string // nil for the root itself
}

type tree struct {
	files   map[string][]string
	roots   map[string]bool              // crate roots: they declare child modules in their own directory
	modOf   map[string]module            // each file reached from a crate root, by the first root to reach it
	modules map[string]map[string]string // crate root → module path (a::b, "" for the root) → file
	crates  map[string]string            // crate name → its library's root file
	seen    map[[2]string]bool           // {root, file} walked
}

// build reads the crates of configs and walks each crate root's module tree;
// libraries first, so a file two roots declare belongs to the library.
func build(files map[string][]string, configs map[string]string) *tree {
	t := &tree{files: files, roots: map[string]bool{}, modOf: map[string]module{},
		modules: map[string]map[string]string{}, crates: map[string]string{}, seen: map[[2]string]bool{}}
	byDir := map[string]crate{}
	for p, toml := range configs {
		if path.Base(p) != "Cargo.toml" {
			continue
		}
		if c, ok := readCargo(dir(p), toml); ok {
			byDir[c.dir] = c
		}
	}
	var libs, others []string
	for f := range files {
		c, ok := owner(f, byDir)
		switch {
		case !ok || !c.isRoot(f):
		case f == c.lib:
			libs = append(libs, f)
			for _, n := range c.names {
				if cur, taken := t.crates[n]; !taken || f < cur { // of two crates named alike, the first by path
					t.crates[n] = f
				}
			}
		default:
			others = append(others, f)
		}
	}
	slices.Sort(libs)
	slices.Sort(others)
	roots := slices.Concat(libs, others)
	for _, r := range roots {
		t.roots[r] = true
		t.modules[r] = map[string]string{}
	}
	for _, r := range roots { // once all are known: modFile reads them
		t.walk(r, r, nil)
	}
	return t
}

// owner is the crate f belongs to: the nearest directory above it with a package.
func owner(f string, byDir map[string]crate) (crate, bool) {
	for d := dir(f); ; d = dir(d) {
		if c, ok := byDir[d]; ok {
			return c, true
		}
		if d == "" {
			return crate{}, false
		}
	}
}

// walk places f at module mod of the crate rooted at root, then the modules it declares.
func (t *tree) walk(root, f string, mod []string) {
	if t.seen[[2]string{root, f}] { // lib.rs may declare `mod lib;`, naming itself
		return
	}
	t.seen[[2]string{root, f}] = true
	t.modules[root][strings.Join(mod, "::")] = f
	if _, ok := t.modOf[f]; !ok {
		t.modOf[f] = module{root, mod}
	}
	for _, fact := range t.files[f] {
		if name, ok := strings.CutPrefix(fact, "mod:"); ok {
			if child := t.modFile(f, name); child != "" {
				t.walk(root, child, slices.Concat(mod, strings.Split(name, "/")))
			}
		}
	}
}

// modFile is the file `mod name;` in f names (name is outer/name inside an
// inline mod outer): name.rs or name/mod.rs in the directory of f's child
// modules, which is f's own for a crate root, mod.rs, and lib.rs and main.rs
// outside any crate, and a/ for any other a.rs (lib.rs that `mod lib;` names too).
func (t *tree) modFile(f, name string) string {
	d := dir(f)
	if base := path.Base(f); !t.roots[f] && base != "mod.rs" && (len(t.modOf[f].path) > 0 || base != "lib.rs" && base != "main.rs") {
		d = path.Join(d, strings.TrimSuffix(base, ".rs"))
	}
	for _, c := range []string{path.Join(d, name+".rs"), path.Join(d, name, "mod.rs")} {
		if _, ok := t.files[c]; ok {
			return c
		}
	}
	return ""
}

// target is the file holding what the path segs, used in src, names: the
// module of its longest prefix that is one; "" outside the repository.
func (t *tree) target(src string, segs []string) string {
	m, placed := t.modOf[src]
	switch segs[0] {
	case "crate":
		if placed {
			return t.lookup(m.root, nil, segs[1:])
		}
	case "self", "super":
		if !placed {
			return ""
		}
		base := m.path
		if segs[0] == "self" {
			segs = segs[1:]
		}
		for len(segs) > 0 && segs[0] == "super" {
			if len(base) == 0 {
				return ""
			}
			base, segs = base[:len(base)-1], segs[1:]
		}
		return t.lookup(m.root, base, segs)
	default:
		if placed {
			if _, child := t.modules[m.root][strings.Join(slices.Concat(m.path, segs[:1]), "::")]; child {
				return t.lookup(m.root, m.path, segs)
			}
		}
		if lib, ok := t.crates[segs[0]]; ok {
			return t.lookup(lib, nil, segs[1:])
		}
	}
	return ""
}

// lookup is the file of the longest module base::rest[:k] of the crate rooted at root.
func (t *tree) lookup(root string, base, rest []string) string {
	for k := len(rest); k >= 0; k-- {
		if f, ok := t.modules[root][strings.Join(slices.Concat(base, rest[:k]), "::")]; ok {
			return f
		}
	}
	return ""
}

// dir is the directory of a repository path, "" at the root.
func dir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}
