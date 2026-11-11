// Package python resolves a Python file's imports to the files they name:
// absolute modules from the repository root or a src/ tree, and relative
// imports against the importing file's package.
package python

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Resolve returns the edges {src, dst} that files' Python import facts name:
// "module:a.b" for `import a.b`, "from:a.b" for `from a.b import ...`, and
// "name:a.b.C" for the name itself, a submodule when one exists. A module
// resolves under the repository root and under each src/ tree (or a tree a
// pyproject.toml, setup.cfg or setup.py names), a package's __init__.py
// included.
func Resolve(files map[string][]string, configs map[string]string) [][2]string {
	mods, fileMods := modules(files, roots(files, configs))
	seen := map[[2]string]bool{}
	var out [][2]string
	for src, facts := range files {
		if !strings.HasSuffix(src, ".py") {
			continue
		}
		for _, f := range facts {
			for _, dst := range targets(src, f, mods, fileMods) {
				if dst == src || seen[[2]string{src, dst}] {
					continue
				}
				seen[[2]string{src, dst}] = true
				out = append(out, [2]string{src, dst})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// roots are the directories a module path may start at: the repository root,
// each src/ tree, and those a Python package setting names.
func roots(files map[string][]string, configs map[string]string) []string {
	set := map[string]bool{"": true}
	for p := range files {
		for i, part := range strings.Split(p, "/") {
			if part == "src" {
				set[strings.Join(strings.Split(p, "/")[:i+1], "/")] = true
			}
		}
	}
	for p, text := range configs {
		dir := path.Dir(p)
		if dir == "." {
			dir = ""
		}
		for _, where := range wheres(path.Base(p), text) {
			set[join(dir, where)] = true
		}
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) }) // the deepest first
	return out
}

var whereRe = regexp.MustCompile(`(?m)^\s*where\s*=\s*["']?([^"'\n]+)`)

// whereInlineRe finds a where= inside a call, as setup.py's find_packages.
var whereInlineRe = regexp.MustCompile(`where\s*=\s*["']([^"']+)["']`)

// wheres are the source roots a Python package setting names, relative to its
// file's directory.
func wheres(base, text string) []string {
	switch base {
	case "pyproject.toml":
		var m struct {
			Tool struct {
				Setuptools struct {
					PackageDir map[string]string `toml:"package-dir"`
					Packages   struct {
						Find struct {
							Where []string
						} `toml:"find"`
					} `toml:"packages"`
				} `toml:"setuptools"`
				Poetry struct {
					Packages []struct{ From string }
				} `toml:"poetry"`
			}
		}
		if toml.Unmarshal([]byte(text), &m) != nil {
			return nil
		}
		var out []string
		for _, dir := range m.Tool.Setuptools.PackageDir {
			out = append(out, dir)
		}
		out = append(out, m.Tool.Setuptools.Packages.Find.Where...)
		for _, p := range m.Tool.Poetry.Packages {
			out = append(out, p.From)
		}
		return out
	case "setup.cfg":
		var out []string
		for _, m := range whereRe.FindAllStringSubmatch(text, -1) {
			for _, s := range strings.Split(m[1], ",") {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case "setup.py":
		var out []string
		for _, m := range whereInlineRe.FindAllStringSubmatch(text, -1) {
			out = append(out, m[1])
		}
		return out
	}
	return nil
}

// modules maps each module name to its file, and each file to the modules it
// is known by under the roots: root/pkg/mod.py is pkg.mod, root/pkg/__init__.py
// is pkg.
func modules(files map[string][]string, roots []string) (map[string]string, map[string][]string) {
	mods, fileMods := map[string]string{}, map[string][]string{}
	paths := make([]string, 0, len(files))
	for p := range files {
		if strings.HasSuffix(p, ".py") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		for _, root := range roots {
			rel := p
			if root != "" {
				if !strings.HasPrefix(p, root+"/") {
					continue
				}
				rel = strings.TrimPrefix(p, root+"/")
			}
			name := moduleName(rel)
			if name == "" {
				continue
			}
			if cur, ok := mods[name]; !ok || strings.HasSuffix(p, "/__init__.py") && !strings.HasSuffix(cur, "/__init__.py") { // a package wins over a module of the same name
				mods[name] = p
			}
			if !contains(fileMods[p], name) {
				fileMods[p] = append(fileMods[p], name)
			}
		}
	}
	return mods, fileMods
}

// moduleName is the module a path relative to a root names.
func moduleName(rel string) string {
	rel = strings.TrimSuffix(rel, ".py")
	if rel == "__init__" {
		return ""
	}
	rel = strings.TrimSuffix(rel, "/__init__")
	return strings.ReplaceAll(rel, "/", ".")
}

// targets are the files a fact in file src names.
func targets(src, fact string, mods map[string]string, fileMods map[string][]string) []string {
	switch {
	case strings.HasPrefix(fact, "module:"):
		return at(mods, strings.TrimPrefix(fact, "module:"))
	case strings.HasPrefix(fact, "from:"):
		mod := strings.TrimPrefix(fact, "from:")
		if strings.HasPrefix(mod, ".") {
			var out []string
			for _, cand := range relative(src, mod, fileMods) {
				out = append(out, at(mods, cand)...)
			}
			return out
		}
		return at(mods, mod)
	case strings.HasPrefix(fact, "name:"):
		mod := strings.TrimPrefix(fact, "name:")
		if strings.HasPrefix(mod, ".") {
			var out []string
			for _, cand := range relative(src, mod, fileMods) {
				if got := at(mods, cand); len(got) > 0 { // a submodule
					out = append(out, got...)
				} else {
					out = append(out, at(mods, parent(cand))...)
				}
			}
			return out
		}
		if got := at(mods, mod); len(got) > 0 { // a submodule
			return got
		}
		return at(mods, parent(mod)) // else the module holding the name
	}
	return nil
}

// at is the file a module names.
func at(mods map[string]string, mod string) []string {
	if p, ok := mods[mod]; ok {
		return []string{p}
	}
	return nil
}

// relative resolves a dotted-relative module (".x", "..pkg", ".pkg.y")
// against src's package to the module names it may mean. A file pkg/mod.py is
// in pkg; pkg/__init__.py is pkg.
func relative(src, mod string, fileMods map[string][]string) []string {
	suffix := strings.TrimLeft(mod, ".")
	dots := len(mod) - len(suffix)
	var out []string
	for _, own := range fileMods[src] {
		pkg := own
		if !strings.HasSuffix(src, "/__init__.py") && src != "__init__.py" {
			pkg = parent(own)
		}
		for range dots - 1 {
			pkg = parent(pkg)
		}
		cand := pkg
		if suffix != "" {
			cand = join(pkg, suffix)
		}
		if !contains(out, cand) {
			out = append(out, cand)
		}
	}
	return out
}

// parent is module without its last part.
func parent(mod string) string {
	if i := strings.LastIndex(mod, "."); i >= 0 {
		return mod[:i]
	}
	return ""
}

// join puts rel under dir with one dot.
func join(dir, rel string) string {
	if dir == "" {
		return rel
	}
	if rel == "" {
		return dir
	}
	return dir + "." + rel
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
