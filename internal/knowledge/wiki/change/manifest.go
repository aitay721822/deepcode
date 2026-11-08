package change

import (
	"path"
	"slices"
	"strings"
	"unicode"
)

// names are the manifests known by name, in lower case: the files that list a
// project's dependencies and build, which change often and of which a page
// cites a few lines.
var names = map[string]bool{"package.json": true, "go.mod": true, "cargo.toml": true, "pom.xml": true,
	"build.gradle": true, "build.gradle.kts": true, "settings.gradle": true, "settings.gradle.kts": true,
	"directory.build.props": true, "directory.packages.props": true, "packages.config": true, "global.json": true}

// Is reports whether file p is a manifest or a .NET project or solution file.
func Is(p string) bool {
	name := strings.ToLower(path.Base(p))
	return names[name] || slices.Contains([]string{".csproj", ".vbproj", ".fsproj", ".sln", ".slnx"}, path.Ext(name))
}

// words are the names and versions in line, as "body-parser" and "2.3.0"; a
// run of punctuation, as "--" or "//", is none.
func words(line string) []string {
	return slices.DeleteFunc(strings.FieldsFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-@/", r)
	}), func(w string) bool {
		return !strings.ContainsFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) })
	})
}
