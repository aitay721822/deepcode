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

type Images struct{ Go, TypeScript, Python, Rust, Java, CSharp string }

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
	emptyDocuments                      bool
	rejectParentDocuments               bool
	syntheticConstructors               bool
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
		emptyDocuments: true, rejectParentDocuments: true, allowEmpty: true,
		script: `if [ "$2" = infer ]; then
  printf '%s' '{"compilerOptions":{"allowJs":true,"jsx":"preserve"},"include":["/src/**/*"],"exclude":["/src/**/node_modules/**","/src/**/vendor/**"]}' > /tmp/tsconfig.json
  scip-typescript index /tmp/tsconfig.json --infer-tsconfig --no-progress-bar --output /tmp/index.scip >&2
else
  config=tsconfig.json
  [ -f "$config" ] || config=jsconfig.json
  node -e 'const ts = require("/usr/local/lib/node_modules/@sourcegraph/scip-typescript/node_modules/typescript"); const path = require("path"); const file = path.resolve(process.argv[1]); const source = ts.readConfigFile(file, ts.sys.readFile); if (source.error) { console.error(source.error.messageText); process.exit(1); } const parsed = ts.parseJsonConfigFileContent(source.config, ts.sys, path.dirname(file), undefined, file); const errors = parsed.errors.filter(e => e.code !== 18002 && e.code !== 18003); if (errors.length) { console.error(errors.map(e => e.messageText)); process.exit(1); } process.exit(parsed.fileNames.length ? 0 : 10);' "$config"
  status=$?
  [ "$status" -ne 10 ] || exit 0
  [ "$status" -eq 0 ] || exit "$status"
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
	{
		name: "Rust", tool: "rust-analyzer", image: func(i Images) string { return i.Rust },
		languages: []string{"rust"}, labels: map[string]string{"rust": "rust"}, extensions: map[string]string{".rs": "rust"},
		encoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, roots: rustProjects, identifier: rustIdentifier,
		env: []string{"CARGO_HOME=/tmp/cargo", "CARGO_TARGET_DIR=/tmp/target", "CARGO_NET_OFFLINE=true", "RUSTUP_TOOLCHAIN=1.91.1"},
		script: `set -eu
[ -z "$(find /src -type f \( -path '*/.cargo/config' -o -path '*/.cargo/config.toml' -o -name rust-project.json \))" ] || { echo 'repository Cargo configuration or rust-project.json is not supported' >&2; exit 1; }
cargo metadata --offline --locked --format-version 1 > /tmp/metadata.json
jq -e '.packages | length > 0 and all(.[]; .source == null)' /tmp/metadata.json > /dev/null || { echo 'Rust requires path-only packages' >&2; exit 1; }
printf '%s' '{"cargo":{"buildScripts":{"enable":false,"overrideCommand":["true"]},"sysroot":null,"extraArgs":["--locked","--offline"]},"procMacro":{"enable":false}}' > /tmp/ra.json
/usr/local/bin/rust-analyzer scip "$PWD" --config-path /tmp/ra.json --num-threads 2 --output /tmp/index.scip >&2
cat /tmp/index.scip`,
	},
	{
		name: "Java", tool: "scip-java", image: func(i Images) string { return i.Java },
		languages: []string{"java"}, labels: map[string]string{"java": "java"}, extensions: map[string]string{".java": "java"},
		encoding: scip.PositionEncoding_UnspecifiedPositionEncoding, utf16: true, syntheticConstructors: true,
		roots: func(tree []git.TreeEntry) []project {
			for _, e := range tree {
				if strings.HasSuffix(e.Path, ".java") {
					return []project{{}}
				}
			}
			return nil
		},
		identifier: func(s string) bool { return identifierChars(s, true) },
		env:        []string{"JAVA_TOOL_OPTIONS=-Duser.home=/tmp -Djava.io.tmpdir=/tmp"},
		script: `set -eu
mkdir /tmp/classes /tmp/shards
find /src -mindepth 1 -type d \( -name '.*' -o -name '_*' -o -name vendor -o -name node_modules -o -name build -o -name target \) -prune -o -type f -name '*.java' -print0 | xargs -0 -r /bin/sh -c 'for source do escaped=$(printf "%s" "$source" | sed '\''s/"/\\"/g'\''); printf '\''"%s"\n'\'' "$escaped"; done' sh > /tmp/sources
javac -proc:none -encoding UTF-8 -classpath /opt/scip-javac.jar:/opt/scip-java -J--add-exports=jdk.compiler/com.sun.tools.javac.model=ALL-UNNAMED -J--add-exports=jdk.compiler/com.sun.tools.javac.api=ALL-UNNAMED -J--add-exports=jdk.compiler/com.sun.tools.javac.tree=ALL-UNNAMED -J--add-exports=jdk.compiler/com.sun.tools.javac.util=ALL-UNNAMED -J--add-exports=jdk.compiler/com.sun.tools.javac.code=ALL-UNNAMED '-Xplugin:scip -sourceroot:/src -targetroot:/tmp/shards' -d /tmp/classes @/tmp/sources >&2
/bin/sh /opt/scip-java aggregate --output=/tmp/index.scip /tmp/shards >&2
cat /tmp/index.scip`,
	},
	{
		name: "C#", tool: "scip-dotnet", image: func(i Images) string { return i.CSharp },
		languages: []string{"csharp"}, labels: map[string]string{"C#": "csharp"}, extensions: map[string]string{".cs": "csharp"},
		encoding: scip.PositionEncoding_UnspecifiedPositionEncoding, utf16: true,
		roots: func(tree []git.TreeEntry) []project {
			for _, e := range tree {
				if strings.HasSuffix(e.Path, ".cs") {
					return []project{{}}
				}
			}
			return nil
		},
		identifier: func(s string) bool { return identifierChars(s, false) },
		env:        []string{"DOTNET_CLI_HOME=/tmp", "DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1", "NUGET_PACKAGES=/tmp/nuget"},
		script: `set -eu
mkdir /tmp/project
printf '%s' '<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><EnableDefaultItems>false</EnableDefaultItems><RunAnalyzersDuringBuild>false</RunAnalyzersDuringBuild><EnableNETAnalyzers>false</EnableNETAnalyzers><UseSharedCompilation>false</UseSharedCompilation><ImportDirectoryBuildProps>false</ImportDirectoryBuildProps><ImportDirectoryBuildTargets>false</ImportDirectoryBuildTargets><ImportDirectoryPackagesProps>false</ImportDirectoryPackagesProps><GenerateAssemblyInfo>false</GenerateAssemblyInfo><GenerateTargetFrameworkAttribute>false</GenerateTargetFrameworkAttribute><GenerateDependencyFile>false</GenerateDependencyFile></PropertyGroup><ItemGroup><Compile Include="/src/**/*.cs" Exclude="/src/**/obj/**;/src/**/bin/**;/src/**/.*/**;/src/**/_*/**;/src/**/vendor/**;/src/**/node_modules/**;/src/**/build/**;/src/**/target/**" /></ItemGroup></Project>' > /tmp/project/Index.csproj
printf '%s' '<configuration><packageSources><clear /></packageSources></configuration>' > /tmp/project/NuGet.Config
cd /tmp/project
dotnet restore --configfile NuGet.Config Index.csproj >&2
if ! dotnet build --no-restore Index.csproj > /tmp/build.log 2>&1; then
  cat /tmp/build.log >&2
  grep -q 'error CS8805:' /tmp/build.log || exit 1
  sed -i 's|<TargetFramework>|<OutputType>Exe</OutputType><TargetFramework>|' Index.csproj
  dotnet restore --configfile NuGet.Config Index.csproj >&2
  dotnet build --no-restore Index.csproj >&2
else
  cat /tmp/build.log >&2
fi
if ! dotnet --roll-forward Major /opt/scip/scip-dotnet.dll index Index.csproj --skip-dotnet-restore --working-directory /src --output /tmp/index.scip > /tmp/scip.log 2>&1; then cat /tmp/scip.log >&2; exit 1; fi
cat /tmp/scip.log >&2
if grep -Ei 'WorkspaceDiagnosticKind[.]Failure|fail:' /tmp/scip.log >&2; then exit 1; fi
cat /tmp/index.scip`,
	},
}

func rustProjects(tree []git.TreeEntry) []project {
	var candidates []string
	for _, e := range tree {
		if validPath(e.Path) && path.Base(e.Path) == "Cargo.toml" && !excludedRoot(path.Dir(e.Path)) && !slices.Contains(strings.Split(path.Dir(e.Path), "/"), "target") {
			root := path.Dir(e.Path)
			if root == "." {
				root = ""
			}
			candidates = append(candidates, root)
		}
	}
	slices.Sort(candidates)
	var roots []project
	for _, root := range candidates {
		if !slices.ContainsFunc(roots, func(p project) bool { return p.root == "" || root == p.root || strings.HasPrefix(root, p.root+"/") }) {
			roots = append(roots, project{root: root})
		}
	}
	return roots
}

func rustIdentifier(s string) bool {
	raw := strings.HasPrefix(s, "r#")
	s = strings.TrimPrefix(s, "r#")
	if s == "_" || !identifierChars(s, false) {
		return false
	}
	keywords := "as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while abstract become box do final gen macro override priv try typeof unsized virtual yield"
	if raw {
		return !slices.Contains([]string{"crate", "self", "Self", "super"}, s)
	}
	return !slices.Contains(strings.Fields(keywords), s)
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
	if !identifierChars(s, js) {
		return false
	}
	if js {
		return true
	}
	keywords := "False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield"
	return !slices.Contains(strings.Fields(keywords), s)
}

func identifierChars(s string, js bool) bool {
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
	return true
}
