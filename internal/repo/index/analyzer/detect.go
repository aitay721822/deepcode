package analyzer

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

var byExt = map[string]string{
	".go": "go",
	".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "typescript", ".mts": "typescript", ".cts": "typescript", ".tsx": "tsx",
	".cs": "csharp", ".vb": "vbnet", ".java": "java", ".rs": "rust",
	// indexed as plain text: chunked by lines, no symbols
	".md": "markdown", ".mdx": "markdown", ".rst": "text", ".txt": "text",
	".json": "json", ".jsonc": "json", ".yaml": "yaml", ".yml": "yaml", ".toml": "toml", ".ini": "ini",
	".xml": "xml", ".html": "html", ".htm": "html", ".vue": "html", ".svelte": "html",
	".css": "css", ".scss": "scss", ".less": "less", ".properties": "properties",
	".py": "python", ".rb": "ruby", ".kt": "kotlin", ".kts": "kotlin", ".fs": "fsharp",
	".swift": "swift", ".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp", ".hpp": "cpp",
	".php": "php", ".scala": "scala", ".dart": "dart", ".lua": "lua", ".ex": "elixir", ".exs": "elixir",
	".sh": "bash", ".bash": "bash", ".ps1": "powershell", ".sql": "sql", ".proto": "protobuf",
	".graphql": "graphql", ".gql": "graphql", ".tf": "hcl", ".gradle": "gradle", ".cmake": "cmake",
	// .NET, Core and Framework alike: projects and solutions, configuration
	// (web.config, app.config, packages.config, NuGet.config), resources and views
	".csproj": "msbuild", ".vbproj": "msbuild", ".fsproj": "msbuild", ".props": "msbuild", ".targets": "msbuild",
	".sln": "sln", ".slnx": "xml", ".config": "xml", ".resx": "xml", ".xaml": "xml",
	".cshtml": "razor", ".razor": "razor", ".aspx": "aspnet", ".ascx": "aspnet", ".asax": "aspnet", ".asmx": "aspnet",
}

var byName = map[string]string{
	"dockerfile": "dockerfile", "makefile": "makefile", "go.mod": "go.mod", "readme": "text",
	"license": "text", "cmakelists.txt": "cmake",
}

var skippedNames = map[string]bool{
	"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "go.sum": true,
	"cargo.lock": true, "poetry.lock": true, "composer.lock": true, "bun.lockb": true,
	"project.assets.json": true, // NuGet restore's, in obj/
}

var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "third_party": true, "dist": true,
	"coverage": true, "__pycache__": true, ".venv": true, "venv": true, "bower_components": true,
	".next": true, ".nuxt": true, ".gradle": true,
}

// nugetPackage is a folder of the .NET Framework's packages/ directory: a
// package's id and version, Newtonsoft.Json.13.0.3.
var nugetPackage = regexp.MustCompile(`^[A-Za-z_][\w.-]*?\.\d+\.\d+(\.\d+){0,2}([-+][\w.-]+)?$`)

// output reports a directory, dirs[i], that holds build output only by the
// folders inside it, since its name alone is source elsewhere: bin/Debug and
// obj/x64/Release (.NET, where Rust's src/bin/debug is a program and Go has a
// package obj), target/release (Cargo, Maven) and build/classes (Gradle, where
// build/ holds scripts elsewhere) outside src/ (where they are Java packages),
// packages/Name.1.2.3 (NuGet, where a JavaScript monorepo keeps its packages).
func output(dirs []string, i int) bool {
	if i+1 >= len(dirs) {
		return false
	}
	switch next := dirs[i+1]; dirs[i] {
	case "bin", "obj":
		if i+2 < len(dirs) && platforms[strings.ToLower(next)] {
			next = dirs[i+2]
		}
		return next == "Debug" || next == "Release" // .NET's own spelling
	case "target":
		return targetOutput[next] && !slices.Contains(dirs[:i], "src")
	case "build":
		return buildOutput[next] && !slices.Contains(dirs[:i], "src")
	case "packages":
		return nugetPackage.MatchString(next)
	}
	return false
}

// platforms are the folders .NET puts between bin or obj and the configuration.
var platforms = map[string]bool{"x64": true, "x86": true, "arm64": true, "arm": true, "anycpu": true}

// targetOutput and buildOutput are what Cargo and Maven put in target/, and
// Gradle in build/.
var (
	targetOutput = map[string]bool{"debug": true, "release": true, "classes": true, "test-classes": true,
		"generated-sources": true, "generated-test-sources": true, "surefire-reports": true}
	buildOutput = map[string]bool{"classes": true, "generated": true, "intermediates": true, "kotlin": true, "reports": true}
)

// Detect returns the language of a repository path and whether it should be
// indexed at all. Vendored, built and lock files are skipped.
func Detect(p string) (string, bool) {
	dirs := strings.Split(p, "/")
	dirs = dirs[:len(dirs)-1]
	for i, d := range dirs {
		if skippedDirs[d] || output(dirs, i) {
			return "", false
		}
	}
	base := strings.ToLower(path.Base(p))
	if skippedNames[base] || strings.Contains(base, ".min.") || strings.HasSuffix(base, ".map") ||
		strings.Contains(base, "proj.nuget.") || // restore's output in obj/: Shop.csproj.nuget.g.props
		strings.HasSuffix(base, ".dll.config") || strings.HasSuffix(base, ".exe.config") { // a build's copy of app.config, in a web project's bin/ too
		return "", false
	}
	if lang, ok := byName[base]; ok {
		return lang, true
	}
	lang, ok := byExt[path.Ext(base)]
	return lang, ok
}

// generatedNames are the file name endings .NET's tools write: designers,
// source generators' output, assembly info.
var generatedNames = []string{".designer.cs", ".designer.vb", ".g.cs", ".g.i.cs", ".assemblyinfo.cs", "/assemblyinfo.cs", "/assemblyinfo.vb"}

// goGenerated is Go's mark of a generated file (go help generate).
var goGenerated = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.\r?$`)

// Generated reports a file a tool wrote, by its name or folder (a service
// reference's proxy code) or a mark at its top, .NET's <auto-generated> in
// its code, Go's in code of any language: it is indexed as text, as tests
// are, with no symbols or imports. Documents quoting a mark are not.
func Generated(p, content string) bool {
	name := "/" + strings.ToLower(p)
	if strings.Contains(name, "/service references/") || strings.Contains(name, "/connected services/") {
		return true
	}
	for _, s := range generatedNames {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	head := content[:min(len(content), 2048)]
	switch byExt[path.Ext(name)] {
	case "csharp", "vbnet", "fsharp":
		return strings.Contains(strings.ToLower(head), "<auto-generated")
	case "markdown", "text":
		return false
	}
	return goGenerated.MatchString(head)
}

// SecondaryGlobs match the paths of tests, examples, benchmarks and docs, as
// SQLite's GLOB does (* spans slashes, case counts): they repeat a question's
// words more often than the code that implements them, and what they import
// shows how the code is used, not how its parts depend on each other. Whole
// segments only, so latest.go or contest.go stay code.
// ponytail: fixed path globs; learn them per repository if other layouts rank badly.
var SecondaryGlobs = []string{
	"*_test.*", "*.test.*", "*.spec.*", "test_*", "*/test_*", // file names: Go, JS/TS, Python
	"*Tests.cs", "*Test.cs", "*Tests.vb", "*Test.java", "*Tests.java", "*IT.java", // C#, VB, Java (src/test/ below)
	"test/*", "*/test/*", "tests/*", "*/tests/*", "__tests__/*", "*/__tests__/*", "spec/*", "*/spec/*", "e2e/*", "*/e2e/*",
	"*Tests/*", "benches/*", "*/benches/*", // .NET test projects (Foo.UnitTests/), Rust benchmarks
	"docs/*", "*/docs/*", "examples/*", "*/examples/*", "_examples/*", "*/_examples/*", "*.md", "*.mdx",
}

// Secondary reports whether path p matches one of SecondaryGlobs.
func Secondary(p string) bool {
	for _, g := range SecondaryGlobs {
		if glob(g, p) {
			return true
		}
	}
	return false
}

// glob matches s against pattern whose only wildcard is *, which spans any
// run of characters, slashes included, as in SQLite's GLOB.
func glob(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return len(parts) == 1 && s == "" || len(parts) > 1 && len(s) >= len(last) && strings.HasSuffix(s, last)
}
