package entry

import (
	"slices"
	"strings"
	"testing"
)

// TestEdges: each main goes to the nearest project file of its language's
// build, both when one folder holds two, past a nearer one of another build;
// a test's main or project to none, nor a main with no project of its build,
// nor a JavaScript function main; a crate's library and a package's main,
// bins and exports (their import and require, not their types) to their own
// project file; a workspace with no [package] has no library, and a built
// file the repository lacks is no entry, nor a pattern's or a manifest's.
func TestEdges(t *testing.T) {
	langs := map[string]string{
		"src/AppHost/AppHost.cs": "csharp", "src/Api/Program.cs": "csharp", "src/Api/Models/Item.cs": "csharp", "tests/Api.Tests/Main.cs": "csharp",
		"crates/core/src/lib.rs": "rust", "crates/cli/src/main.rs": "rust",
		"src/main/java/app/App.java": "java",
		"packages/ui/src/index.ts":   "typescript", "packages/ui/src/server.ts": "typescript", "index.js": "javascript",
		"packages/ui/src/cli.ts": "typescript", "web/cmd/tool/main.go": "go", "scratch/Gen.cs": "csharp", "tests/fixtures/pkg/index.js": "javascript",
	}
	configs := map[string]string{
		"src/AppHost/AppHost.csproj": "<Project/>", "src/Api/Api.csproj": "<Project/>", "src/Api/Api.Legacy.csproj": "<Project/>", "tests/Api.Tests/Api.Tests.csproj": "<Project/>",
		"Cargo.toml": "[workspace]\nmembers = [\"crates/*\"]\n", "crates/core/Cargo.toml": "[package]\nname = \"core\"\n", "crates/cli/Cargo.toml": "[package]\nname = \"cli\"\n",
		"pom.xml":                  "<project/>",
		"package.json":             `{"name": "root", "private": true}`,
		"packages/ui/package.json": `{"main": "./dist/index.js", "bin": {"ui": "./src/cli.js"}, "exports": {".": {"types": "./src/index.d.ts", "import": "./src/index.js", "require": "./src/server.js"}, "./package.json": "./package.json", "./*": "./src/*.js"}}`,
		"go.mod":                   "module x", "web/package.json": `{"name": "web"}`, "tests/fixtures/pkg/package.json": `{"main": "index.js"}`,
	}
	mains := []string{"src/AppHost/AppHost.cs", "src/Api/Program.cs", "tests/Api.Tests/Main.cs", "crates/cli/src/main.rs", "src/main/java/app/App.java",
		"packages/ui/src/cli.ts", "web/cmd/tool/main.go", "scratch/Gen.cs"}
	exists := func(src, spec string) string { // as the graph finds a file: the .ts a .js compiles from
		p := strings.TrimPrefix(strings.TrimPrefix(src[:strings.LastIndex(src, "/")+1]+spec, "./"), "/")
		p = strings.ReplaceAll(p, "/./", "/")
		for _, c := range []string{p, strings.TrimSuffix(p, ".js") + ".ts"} {
			if langs[c] != "" {
				return c
			}
		}
		return ""
	}
	var got []string
	for _, e := range Edges(langs, configs, mains, exists) {
		got = append(got, e[0]+" > "+e[1])
	}
	want := []string{
		"crates/cli/Cargo.toml > crates/cli/src/main.rs",
		"crates/core/Cargo.toml > crates/core/src/lib.rs",
		"go.mod > web/cmd/tool/main.go",
		"package.json > index.js",
		"packages/ui/package.json > packages/ui/src/cli.ts",
		"packages/ui/package.json > packages/ui/src/index.ts",
		"packages/ui/package.json > packages/ui/src/server.ts",
		"pom.xml > src/main/java/app/App.java",
		"src/Api/Api.Legacy.csproj > src/Api/Program.cs",
		"src/Api/Api.csproj > src/Api/Program.cs",
		"src/AppHost/AppHost.csproj > src/AppHost/AppHost.cs",
	}
	if !slices.Equal(got, want) {
		t.Errorf("edges:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if m := modules(`not json`); m != nil {
		t.Errorf("a broken package.json: %v", m)
	}
	if Edges(nil, nil, nil, exists) != nil {
		t.Error("edges with no files")
	}
	for _, c := range []struct{ pkg, want string }{
		{`{"exports": "./lib/main.js"}`, "index.js ./lib/main.js"},
		{`{"exports": {".": ["./a.js", {"types": "./b.d.ts", "import": "./b.js"}]}}`, "index.js ./a.js ./b.js"}, // a fallback array
		{`{"main": "lib/x.js", "exports": {"./sub": "./sub.js"}}`, "lib/x.js ./sub.js"},                         // subpaths, no "."
		{`{"exports": {"import": "./i.js", "default": "./d.js"}}`, "index.js ./d.js ./i.js"},                    // conditions only
		{`{"bin": "./cli.js"}`, "index.js ./cli.js"},
		{`{"bin": {"b": "./b.js", "a": "a.js"}, "exports": {"./x": null}}`, "index.js a.js ./b.js"}, // a bin each; an excluded subpath
	} {
		if m := strings.Join(modules(c.pkg), " "); m != c.want {
			t.Errorf("modules(%s) = %s, want %s", c.pkg, m, c.want)
		}
	}
}
