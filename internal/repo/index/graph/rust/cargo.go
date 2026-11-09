package rust

import (
	"path"
	"strings"
)

// crate is a Cargo package: a Cargo.toml with a [package].
type crate struct {
	dir   string   // its Cargo.toml's directory, "" at the repository's root
	names []string // what other crates use it by: the package's name and its [lib] name, - as _
	lib   string   // its library's root file, src/lib.rs unless [lib] path says another
	bins  []string // the root files [[bin]] path names
}

// readCargo reads the Cargo.toml in dir; false for a workspace root with no
// [package] of its own.
// ponytail: line-based, `key = "value"` under a [section] header; a dotted
// key (package.name = …) or an inline table goes unread, as does a
// [[test]] or [[example]] path. A TOML reader if a crate needs those.
func readCargo(dir, toml string) (crate, bool) {
	c := crate{dir: dir, lib: path.Join(dir, "src/lib.rs")}
	pkg := false
	section := ""
	for _, line := range strings.Split(toml, "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] \t")
			pkg = pkg || section == "package"
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = str(strings.TrimSpace(v))
		switch section + "." + strings.TrimSpace(k) {
		case "package.name", "lib.name":
			c.names = append(c.names, strings.ReplaceAll(v, "-", "_"))
		case "lib.path":
			c.lib = path.Join(dir, v)
		case "bin.path":
			c.bins = append(c.bins, path.Join(dir, v))
		}
	}
	return c, pkg
}

// Lib is the library root file of the package whose Cargo.toml in dir holds
// toml, src/lib.rs unless [lib] path says another; false for a workspace
// root with no [package] of its own.
func Lib(dir, toml string) (string, bool) {
	c, ok := readCargo(dir, toml)
	return c.lib, ok
}

// str is a TOML string's text without its quotes, "" for any other value.
func str(v string) string {
	if len(v) < 2 || v[0] != '"' && v[0] != '\'' {
		return ""
	}
	s, _, _ := strings.Cut(v[1:], v[:1])
	return s
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
