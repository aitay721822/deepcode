// Package test holds checks that span the whole repository.
package test

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestFolderLimits enforces the layout rule: every folder holds at most five
// files and at most five subfolders; past that, split it by business area.
// .github is GitHub's, which reads workflows from .github/workflows only.
func TestFolderLimits(t *testing.T) {
	files := map[string]int{}
	dirs := map[string]map[string]bool{}
	for _, f := range repoFiles(t) {
		if strings.HasPrefix(f, ".github/") {
			continue
		}
		dir := path.Dir(f)
		files[dir]++
		for d := dir; d != "."; d = path.Dir(d) {
			parent := path.Dir(d)
			if dirs[parent] == nil {
				dirs[parent] = map[string]bool{}
			}
			dirs[parent][d] = true
		}
	}
	for dir, n := range files {
		if n > 5 {
			t.Errorf("%s/ holds %d files (max 5)", dir, n)
		}
	}
	for dir, subs := range dirs {
		if len(subs) > 5 {
			t.Errorf("%s/ holds %d folders (max 5)", dir, len(subs))
		}
	}
}

// TestFileLength enforces the length rule: no file is longer than 500 lines;
// past that, split it by responsibility. The lockfile npm writes is not ours
// to split.
func TestFileLength(t *testing.T) {
	for _, f := range repoFiles(t) {
		if f == "web/package-lock.json" {
			continue
		}
		b, err := os.ReadFile("../" + f)
		if err != nil {
			t.Fatal(err)
		}
		lines := bytes.Count(b, []byte("\n"))
		if len(b) > 0 && b[len(b)-1] != '\n' {
			lines++ // a last line without a newline
		}
		if lines > 500 {
			t.Errorf("%s has %d lines (max 500)", f, lines)
		}
	}
}

// repoFiles lists the files git would track, from the repository root, so
// build output and data are left out, as are files deleted but still indexed.
func repoFiles(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", "..", "ls-files", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	var files []string
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if _, err := os.Stat("../" + f); err == nil {
			files = append(files, f)
		}
	}
	return files
}

// TestBackendDependencies enforces the README's backend dependency rule:
// platform <- repo <- knowledge, access on its own, all of them <- app/importer
// <- app/server <- app <- cmd, never the other way; cmd also uses
// app/server/webui, platform/storage and access/account with its store directly.
func TestBackendDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "deepcode/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	// What each area may import besides itself: an area, or one package.
	may := map[string][]string{
		"internal/platform":     nil,
		"internal/repo":         {"internal/platform"},
		"internal/knowledge":    {"internal/platform", "internal/repo"},
		"internal/access":       nil,
		"internal/app/importer": {"internal/platform", "internal/repo", "internal/knowledge", "internal/access"},
		"internal/app/server":   {"internal/platform", "internal/repo", "internal/knowledge", "internal/access", "internal/app/importer"},
		"internal/app":          {"internal/platform", "internal/repo", "internal/knowledge", "internal/access", "internal/app/importer", "internal/app/server"},
		"cmd":                   {"internal/app", "internal/app/server/webui", "internal/platform/storage", "internal/access/account", "internal/access/account/store"},
	}
	area := func(pkg string) string { // the longest area holding pkg
		in := ""
		for a := range may {
			if (pkg == a || strings.HasPrefix(pkg, a+"/")) && len(a) > len(in) {
				in = a
			}
		}
		return in
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		pkg := strings.TrimPrefix(f[0], "deepcode/")
		if pkg == "test" || strings.HasPrefix(pkg, "test/") {
			continue // tests may use anything
		}
		from := area(pkg)
		if from == "" {
			t.Errorf("%s is in no area of the dependency rule", pkg)
			continue
		}
		for _, imp := range f[1:] {
			imp, ours := strings.CutPrefix(imp, "deepcode/")
			if ours && area(imp) != from && !slices.Contains(may[from], area(imp)) && !slices.Contains(may[from], imp) {
				t.Errorf("%s imports %s", pkg, imp)
			}
		}
	}
}

// TestPersistenceLayer enforces the layering rule: SQL lives only in the
// persistence layer, a package whose folder is named store (or ends in
// store), and in platform/storage, which opens, migrates and backs up the
// database. Business logic and the API layer call those packages; only the
// composition root (internal/app) and cmd hold the *sql.DB the stores share.
func TestPersistenceLayer(t *testing.T) {
	runs := regexp.MustCompile(`\b(?:QueryContext|QueryRowContext|ExecContext|BeginTx|PrepareContext)\(|\b(?:SELECT|INSERT INTO|DELETE FROM)\b`)
	for _, f := range repoFiles(t) {
		dir := path.Dir(f)
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "test/") ||
			strings.HasSuffix(path.Base(dir), "store") || dir == "internal/platform/storage" {
			continue
		}
		b, err := os.ReadFile("../" + f)
		if err != nil {
			t.Fatal(err)
		}
		if runs.Match(b) {
			t.Errorf("%s runs SQL outside the persistence layer", f)
		}
		if bytes.Contains(b, []byte(`"database/sql"`)) && dir != "internal/app" && !strings.HasPrefix(dir, "cmd/") {
			t.Errorf("%s imports database/sql outside the persistence layer", f)
		}
	}
}

// TestFrontendLayers enforces the README's frontend rule: types at the bottom,
// lib on types, api on both, components on both but never api or pages; pages
// and the entry files compose everything.
func TestFrontendLayers(t *testing.T) {
	const src = "../web/src"
	may := map[string][]string{"types": nil, "lib": {"types"}, "api": {"types", "lib"}, "components": {"types", "lib"}}
	// import './x', ... from './x' (import or export, over several lines too), import('./x')
	relative := regexp.MustCompile(`(?m)(?:^import\s*|\bfrom\s*|\bimport\s*\(\s*)['"](\.[^'"]*)['"]`)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".ts") && !strings.HasSuffix(p, ".tsx") {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		layer, _, _ := strings.Cut(rel, "/")
		allowed, ruled := may[layer]
		if !ruled {
			return nil
		}
		code, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range relative.FindAllStringSubmatch(string(code), -1) {
			to, _, _ := strings.Cut(path.Join(path.Dir(rel), m[1]), "/")
			if to != layer && !slices.Contains(allowed, to) {
				t.Errorf("%s imports %s", rel, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
