package scipimport

import (
	"go/token"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/scip-code/scip/bindings/go/scip"

	"deepcode/internal/repo/git"
)

type Images struct{ Go, TypeScript, Python string }

type project struct {
	root  string
	infer bool
}

type languageSpec struct {
	name, tool                          string
	image                               func(Images) string
	languages                           []string
	labels, extensions                  map[string]string
	encoding                            scip.PositionEncoding
	utf16, anchors, deepest, allowEmpty bool
	anchorSuffix                        scip.Descriptor_Suffix
	anchorName                          string
	roots                               func([]git.TreeEntry) []project
	identifier                          func(string) bool
	script                              string
	env                                 []string
}

var specs = []languageSpec{
	{
		name: "Go", tool: "scip-go", image: func(i Images) string { return i.Go },
		languages: []string{"go"}, labels: map[string]string{"go": "go"}, extensions: map[string]string{".go": "go"},
		encoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, identifier: token.IsIdentifier,
		allowEmpty: true,
		roots: func(tree []git.TreeEntry) []project {
			var p []project
			for _, r := range moduleRoots(tree) {
				p = append(p, project{root: r})
			}
			return p
		},
		script: `packages=$(go list -export ./...) || exit $?
[ -n "$packages" ] || exit 0
exec /usr/local/bin/scip-go index --quiet --skip-tests --skip-implementations --module-version="$1" --output=- ./...`,
		env: []string{"GOCACHE=/tmp/go-cache", "GOMODCACHE=/opt/go-mod", "GOENV=off", "GOWORK=off", "GOPACKAGESDRIVER=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off", "GOFLAGS=-buildvcs=false", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0"},
	},
	{
		name: "TypeScript/JavaScript", tool: "scip-typescript", image: func(i Images) string { return i.TypeScript },
		languages:  []string{"javascript", "typescript", "tsx"},
		labels:     map[string]string{"TypeScript": "typescript", "TypeScriptReact": "tsx", "JavaScript": "javascript", "JavaScriptReact": "javascript"},
		extensions: map[string]string{".ts": "typescript", ".mts": "typescript", ".cts": "typescript", ".tsx": "tsx", ".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript"},
		encoding:   scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart, utf16: true, anchors: true, deepest: true, anchorSuffix: scip.Descriptor_Namespace,
		roots: nodeProjects, identifier: func(s string) bool { return identifier(s, true) },
		script: `if [ "$2" = infer ]; then
  printf '%s' '{"compilerOptions":{"allowJs":true,"jsx":"preserve"},"include":["/src/**/*"],"exclude":["/src/**/node_modules/**","/src/**/vendor/**"]}' > /tmp/tsconfig.json
  scip-typescript index /tmp/tsconfig.json --infer-tsconfig --no-progress-bar --output /tmp/index.scip >&2
else
  config=tsconfig.json
  [ -f "$config" ] || config=jsconfig.json
  scip-typescript index "$config" --no-progress-bar --output /tmp/index.scip >&2
fi || exit $?
cat /tmp/index.scip`,
	},
	{
		name: "Python", tool: "scip-python", image: func(i Images) string { return i.Python },
		languages: []string{"python"}, labels: map[string]string{"": "python"}, extensions: map[string]string{".py": "python"},
		encoding: scip.PositionEncoding_UnspecifiedPositionEncoding, utf16: true, anchors: true, anchorSuffix: scip.Descriptor_Meta, anchorName: "__init__",
		roots: pythonProjects, identifier: func(s string) bool { return identifier(s, false) },
		script: `printf '[]' > /tmp/environment.json
scip-python index --project-name repository --project-version "$1" --environment /tmp/environment.json --output /tmp/index.scip >&2 || exit $?
cat /tmp/index.scip`,
	},
}

func (spec languageSpec) blobs(tree []git.TreeEntry) map[string]string {
	files := make(map[string]string)
	for _, e := range tree {
		if _, ok := spec.extensions[path.Ext(e.Path)]; ok {
			files[e.Path] = e.SHA
		}
	}
	return files
}

func nodeProjects(tree []git.TreeEntry) []project {
	seen := map[string]bool{}
	var roots []project
	for _, e := range tree {
		base := path.Base(e.Path)
		if !validPath(e.Path) || (base != "tsconfig.json" && base != "jsconfig.json") || excludedRoot(path.Dir(e.Path)) {
			continue
		}
		root := path.Dir(e.Path)
		if root == "." {
			root = ""
		}
		if !seen[root] {
			roots = append(roots, project{root: root})
			seen[root] = true
		}
	}
	if len(roots) == 0 {
		for _, e := range tree {
			if slices.Contains([]string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}, path.Ext(e.Path)) && !excludedRoot(path.Dir(e.Path)) {
				return []project{{infer: true}}
			}
		}
	}
	slices.SortFunc(roots, func(a, b project) int { return strings.Compare(a.root, b.root) })
	return roots
}

func pythonProjects(tree []git.TreeEntry) []project {
	for _, e := range tree {
		if strings.HasSuffix(e.Path, ".py") {
			return []project{{}}
		}
	}
	return nil
}

func excludedRoot(dir string) bool {
	if dir == "." {
		return false
	}
	return slices.ContainsFunc(strings.Split(dir, "/"), func(s string) bool {
		return s == "node_modules" || s == "vendor" || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "_")
	})
}

func identifier(s string, js bool) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		start := r == '_' || unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_ID_Start, r) || js && r == '$'
		continuation := unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Pc, r) || unicode.Is(unicode.Other_ID_Continue, r) || js && (r == '\u200c' || r == '\u200d')
		if !start && (i == 0 || !continuation) {
			return false
		}
	}
	keywords := "False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield"
	if js {
		keywords = "await break case catch class const continue debugger default delete do else enum export extends false finally for function if implements import in instanceof interface let new null package private protected public return static super switch this throw true try typeof var void while with yield"
	}
	return !slices.Contains(strings.Fields(keywords), s)
}
