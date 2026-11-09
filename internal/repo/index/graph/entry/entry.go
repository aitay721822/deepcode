// Package entry links each project file to the files its build starts from:
// a program's main, a crate's library root, a package's main module. No
// import names these, so without the link a project file found by a search
// leads nowhere: eShop's AppHost project holds the word Aspire, its
// AppHost.cs the code that orchestrates the services.
package entry

import (
	"cmp"
	"encoding/json"
	"maps"
	"path"
	"slices"
	"strings"

	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/graph/rust"
)

// family is the build a project file p belongs to, "" for none.
func family(p string) string {
	base := strings.ToLower(path.Base(p))
	switch {
	case strings.HasSuffix(base, ".csproj"), strings.HasSuffix(base, ".fsproj"), strings.HasSuffix(base, ".vbproj"):
		return "dotnet"
	case base == "go.mod":
		return "go"
	case base == "cargo.toml":
		return "rust"
	case base == "pom.xml", base == "build.gradle", base == "build.gradle.kts":
		return "jvm"
	case base == "package.json":
		return "js"
	}
	return ""
}

// builds is the family whose project files build a language's programs, a
// main its entry. A package.json names its own (modules): a JavaScript
// function main is any helper.
var builds = map[string]string{"csharp": "dotnet", "go": "go", "rust": "rust", "java": "jvm"}

// Edges are the edges {project file, entry file}: each file of mains, those
// declaring a main or Main (C#'s top-level statements make one), from the
// nearest project file of its language's build at or above it; each crate's
// library root from its Cargo.toml; and each package.json's main, module,
// bins and exports from it, as js finds the file a specifier names (the
// source a built .js compiles from among them). langs maps each file with a
// grammar to its language; configs the project files to their content.
// A test's or an example's main or project is no entry of the project.
func Edges(langs, configs map[string]string, mains []string, js func(src, spec string) string) [][2]string {
	projects := map[string][]string{} // by family
	for p := range configs {
		if f := family(p); f != "" {
			projects[f] = append(projects[f], p)
		}
	}
	var out [][2]string
	for _, m := range mains {
		if analyzer.Aside(m) != "" {
			continue
		}
		for _, p := range nearest(projects[builds[langs[m]]], m) {
			out = append(out, [2]string{p, m})
		}
	}
	for p, content := range configs {
		if analyzer.Aside(p) != "" {
			continue
		}
		switch family(p) {
		case "rust":
			if lib, ok := rust.Lib(dir(p), content); ok && langs[lib] != "" {
				out = append(out, [2]string{p, lib})
			}
		case "js":
			for _, spec := range modules(content) {
				if f := js(p, "./"+strings.TrimPrefix(spec, "./")); f != "" {
					out = append(out, [2]string{p, f})
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b [2]string) int { return cmp.Or(strings.Compare(a[0], b[0]), strings.Compare(a[1], b[1])) })
	return slices.Compact(out)
}

// nearest are those of projects in the deepest folder holding f.
func nearest(projects []string, f string) []string {
	var out []string
	best := -1
	for _, p := range projects {
		d := dir(p)
		if d != "" && !strings.HasPrefix(f, d+"/") {
			continue
		}
		switch {
		case len(d) > best:
			out, best = []string{p}, len(d)
		case len(d) == best:
			out = append(out, p)
		}
	}
	return out
}

// modules are the files a package.json starts from: main and module, else
// index.js as Node takes, each bin, and each export, "." and subpaths alike,
// every condition's but types. A pattern's (./*: ./src/*.js) names no file.
func modules(content string) []string {
	var pkg struct {
		Main    string          `json:"main"`
		Module  string          `json:"module"`
		Bin     json.RawMessage `json:"bin"`
		Exports json.RawMessage `json:"exports"`
	}
	if json.Unmarshal([]byte(content), &pkg) != nil {
		return nil
	}
	out := []string{cmp.Or(pkg.Main, "index.js")}
	if pkg.Module != "" {
		out = append(out, pkg.Module)
	}
	for _, raw := range []json.RawMessage{pkg.Bin, pkg.Exports} {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			out = append(out, paths(v, 0)...)
		}
	}
	return out
}

// paths are the paths an export names, through nested conditions and
// fallback arrays, those for types aside: declarations, not the code.
func paths(v any, depth int) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if depth < 4 {
				out = append(out, paths(e, depth+1)...)
			}
		}
		return out
	case map[string]any:
		var out []string
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if k != "types" && depth < 4 {
				out = append(out, paths(v[k], depth+1)...)
			}
		}
		return out
	}
	return nil
}

// dir is the directory of a repository path, "" at the root.
func dir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}
