package analyzer

import (
	"path"
	"strings"
)

var byExt = map[string]string{
	".go": "go",
	".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "typescript", ".mts": "typescript", ".cts": "typescript", ".tsx": "tsx",
	// indexed as plain text: chunked by lines, no symbols
	".md": "markdown", ".mdx": "markdown", ".rst": "text", ".txt": "text",
	".json": "json", ".jsonc": "json", ".yaml": "yaml", ".yml": "yaml", ".toml": "toml", ".ini": "ini",
	".xml": "xml", ".html": "html", ".htm": "html", ".vue": "html", ".svelte": "html",
	".css": "css", ".scss": "scss", ".less": "less",
	".py": "python", ".rb": "ruby", ".rs": "rust", ".java": "java", ".kt": "kotlin", ".kts": "kotlin",
	".swift": "swift", ".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp", ".hpp": "cpp", ".cs": "csharp",
	".php": "php", ".scala": "scala", ".dart": "dart", ".lua": "lua", ".ex": "elixir", ".exs": "elixir",
	".sh": "bash", ".bash": "bash", ".ps1": "powershell", ".sql": "sql", ".proto": "protobuf",
	".graphql": "graphql", ".gql": "graphql", ".tf": "hcl", ".gradle": "gradle", ".cmake": "cmake",
}

var byName = map[string]string{
	"dockerfile": "dockerfile", "makefile": "makefile", "go.mod": "go.mod", "readme": "text",
	"license": "text", "cmakelists.txt": "cmake",
}

var skippedNames = map[string]bool{
	"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "go.sum": true,
	"cargo.lock": true, "poetry.lock": true, "composer.lock": true, "bun.lockb": true,
}

var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "third_party": true, "dist": true,
	"coverage": true, "__pycache__": true, ".venv": true, "venv": true, "bower_components": true,
	".next": true, ".nuxt": true,
}

// Detect returns the language of a repository path and whether it should be
// indexed at all. Vendored, generated and lock files are skipped.
func Detect(p string) (string, bool) {
	dirs := strings.Split(p, "/")
	for _, d := range dirs[:len(dirs)-1] {
		if skippedDirs[d] {
			return "", false
		}
	}
	base := strings.ToLower(path.Base(p))
	if skippedNames[base] || strings.Contains(base, ".min.") || strings.HasSuffix(base, ".map") {
		return "", false
	}
	if lang, ok := byName[base]; ok {
		return lang, true
	}
	lang, ok := byExt[path.Ext(base)]
	return lang, ok
}
