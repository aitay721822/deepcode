package graph

import (
	"cmp"
	"path"
	"slices"
	"strings"
)

// file is a source file in a language with a grammar, with the modules it
// imports as its source spells them.
type file struct {
	path, lang string
	imports    []string
}

// edge is an import of one file by another: src imports dst.
type edge struct{ src, dst string }

// jsExts are the extensions a JavaScript or TypeScript specifier may leave
// out, in the order a file is looked for.
var jsExts = []string{".ts", ".tsx", ".d.ts", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts"}

// resolve turns the files' imports into edges between them, sorted. A Go
// import path names a directory through a go.mod's module path (modules maps
// each go.mod's directory to its module), and every Go file of that package,
// tests aside; a JavaScript or TypeScript specifier starting with . names a
// file relative to the importer, as Node and TypeScript look it up.
// ponytail: package names (react, @/components via tsconfig paths, Go
// modules of a go.work) stay unresolved; read tsconfig.json's paths if
// repositories that rely on them lose their graph.
func resolve(files []file, modules map[string]string) []edge {
	code := map[string]bool{}
	pkgs := map[string][]string{} // directory → its Go files, tests aside
	for _, f := range files {
		code[f.path] = true
		if f.lang == "go" && !strings.HasSuffix(f.path, "_test.go") {
			pkgs[dir(f.path)] = append(pkgs[dir(f.path)], f.path)
		}
	}
	var out []edge
	for _, f := range files {
		for _, spec := range f.imports {
			var dsts []string
			if f.lang == "go" {
				if d, ok := goPackage(spec, modules); ok {
					dsts = pkgs[d]
				}
			} else if t := jsTarget(f.path, spec, code); t != "" {
				dsts = []string{t}
			}
			for _, d := range dsts {
				if d != f.path {
					out = append(out, edge{f.path, d})
				}
			}
		}
	}
	// Sorted, then each once: two specifiers can name one file (./a, ./a.ts). No set: a monorepo has millions.
	slices.SortFunc(out, func(a, b edge) int { return cmp.Or(cmp.Compare(a.src, b.src), cmp.Compare(a.dst, b.dst)) })
	return slices.Compact(out)
}

// dir is the directory of a repository path, "" at the root.
func dir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// goPackage is the directory of the repository that the Go import path spec
// names: under the go.mod whose module path is the longest prefix of spec.
// ponytail: tries every go.mod for every import; index the modules by path
// and try spec's prefixes if a repository holds hundreds of them.
func goPackage(spec string, modules map[string]string) (string, bool) {
	best, found := "", false
	for d, mod := range modules {
		if !(spec == mod || strings.HasPrefix(spec, mod+"/")) {
			continue
		}
		// The longest module wins; of two go.mod naming one module, the first by path.
		if !found || len(mod) > len(modules[best]) || len(mod) == len(modules[best]) && d < best {
			best, found = d, true
		}
	}
	if !found {
		return "", false
	}
	return strings.TrimPrefix(path.Join(best, strings.TrimPrefix(spec, modules[best])), "/"), true
}

// jsTarget is the file a relative specifier of src names, or "": the path
// itself, then with an extension, then a directory's index file. A .js
// specifier also names the .ts file it compiles from, as TypeScript reads
// one. A query (Vite's ?raw) is not part of the path.
func jsTarget(src, spec string, code map[string]bool) string {
	if spec != "." && spec != ".." && !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
		return ""
	}
	spec, _, _ = strings.Cut(spec, "?")
	base := path.Join(dir(src), spec) // above the root it starts with "..", which no file does
	cands := []string{base}
	switch ext := path.Ext(base); ext {
	case ".js", ".jsx", ".mjs", ".cjs":
		stem := strings.TrimSuffix(base, ext)
		ts := map[string]string{".js": ".ts", ".jsx": ".tsx", ".mjs": ".mts", ".cjs": ".cts"}[ext]
		cands = append(cands, stem+ts, stem+".tsx", stem+".d.ts")
	}
	for _, e := range jsExts {
		cands = append(cands, base+e)
	}
	for _, e := range jsExts {
		cands = append(cands, path.Join(base, "index"+e))
	}
	for _, c := range cands {
		if code[c] {
			return c
		}
	}
	return ""
}

// moduleOf reads the module path a go.mod declares, or "".
func moduleOf(gomod string) string {
	for _, line := range strings.Split(gomod, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`+"`")
		}
	}
	return ""
}
