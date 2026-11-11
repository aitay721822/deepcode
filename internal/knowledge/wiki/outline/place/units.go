// Package place shapes a wiki from the code's structure, with no model: the
// units it divides into (projects, else code-holding folders, thin ones
// merged), each one's cap of pages, the sections they form, the asides'
// shares, the cost ceiling, and where each file of a planned section goes.
package place

import (
	"cmp"
	"maps"
	"path"
	"slices"
	"strings"

	"deepcode/internal/repo/index/analyzer"
	graphstore "deepcode/internal/repo/index/graph/store"
)

// The kinds of code units: a folder holding a project manifest, else a
// folder holding code.
const (
	Project = "project"
	Dir     = "dir"
)

// Asides are the kinds of files outside the code, in the order their
// sections follow the code's.
var Asides = []string{analyzer.Docs, analyzer.Examples, analyzer.Tests, analyzer.Tooling}

// Unit is a part of the repository the wiki plans pages for: a code unit, or
// one aside kind with all its files.
type Unit struct {
	Key    string   // "src/HybridApp/", "./", "a/db/h2/, a/db/mysql/" (merged), or the aside kind
	Kind   string   // Project, Dir, or the aside kind
	Files  []string // in path order
	Code   []string // of Files, those the model picks a page's files from: its code, else all of them
	Cap    int      // the most pages it may take
	Weight float64  // its weighed lines (Weigh); an aside's as its share counts it
	Raw    []string // the keys of the units merged into it, sorted: its identity across snapshots
}

// LinesPerPage is the weighed lines a unit needs for each page past its
// first: about what one of DeepWiki's pages covers.
const LinesPerPage = 100

// maxLines is the most lines one file weighs: a bundled file need not be
// marked generated.
const maxLines = 2000

// light are the languages of the code's styles and data, which weigh a
// quarter, as configuration files do.
var light = []string{"css", "scss", "less", "sql"}

// Code reports whether f is code a page explains: a file of a language with
// a grammar, which declares what it holds, not a build or configuration file.
func Code(f string) bool {
	lang, ok := analyzer.Detect(f)
	return ok && analyzer.HasGrammar(lang) && !analyzer.Config(f)
}

// Weigh is files' weight: each file's lines, at least 1 and at most
// maxLines, a stylesheet, SQL or configuration file a quarter when quarter.
func Weigh(files []string, lines map[string]int, quarter bool) float64 {
	w := 0.0
	for _, f := range files {
		n := float64(min(max(lines[f], 1), maxLines))
		if lang, _ := analyzer.Detect(f); quarter && (analyzer.Config(f) || slices.Contains(light, lang)) {
			n /= 4
		}
		w += n
	}
	return w
}

// manifest reports whether f defines a project's build: go.mod, package.json,
// Cargo.toml, pom.xml, build.gradle(.kts), or a .NET project file. Files that
// set options for a tree (settings.gradle, Directory.Build.props,
// global.json…) define none.
func manifest(f string) bool {
	name := strings.ToLower(path.Base(f))
	return slices.Contains([]string{"go.mod", "package.json", "cargo.toml", "pom.xml", "build.gradle", "build.gradle.kts"}, name) ||
		slices.Contains([]string{".csproj", ".vbproj", ".fsproj"}, path.Ext(name))
}

// Units are the code units of the hand-written files paths, each with its
// cap, and the aside kinds present, each with its files: Raw's, a thin unit
// (under LinesPerPage weighed) merged into the unit it shares the most
// imports with, then the most kin, then the smaller, until none has a
// partner it shares either with.
func Units(paths []string, lines map[string]int, declared map[string][]string, edges []graphstore.Edge) (code []Unit, asides []Unit) {
	code, asides = Raw(paths, lines, declared)
	code = mergeThin(code, edges)
	for i := range code {
		code[i].Cap = capOf(code[i], declared)
	}
	return code, asides
}

// Raw are the code units of the hand-written files paths before any merge,
// each with its cap, and the aside kinds present, each with its files. A
// file is an aside by analyzer.Aside (a root README of 2·LinesPerPage lines
// or more being a guide), else part of its project, the nearest folder above
// it but the root holding a manifest, else of its folder ("./" for the
// root). A unit of only configuration files is tooling.
func Raw(paths []string, lines map[string]int, declared map[string][]string) (code []Unit, asides []Unit) {
	projects := map[string]bool{}
	for _, p := range paths {
		if manifest(p) && path.Dir(p) != "." {
			projects[path.Dir(p)] = true
		}
	}
	byKey, kind, aside := map[string][]string{}, map[string]string{}, map[string][]string{}
	for _, p := range paths {
		k := analyzer.Aside(p)
		if k == analyzer.Tooling && !strings.Contains(p, "/") && strings.HasPrefix(strings.ToLower(p), "readme") && lines[p] >= 2*LinesPerPage {
			k = analyzer.Docs
		}
		if k != "" {
			aside[k] = append(aside[k], p)
			continue
		}
		key, kd := path.Dir(p), Dir
		for d := key; d != "."; d = path.Dir(d) {
			if projects[d] {
				key, kd = d, Project
				break
			}
		}
		key = strings.TrimPrefix(key+"/", "./")
		if key == "" {
			key = "./"
		}
		byKey[key], kind[key] = append(byKey[key], p), kd
	}
	for _, key := range slices.Sorted(maps.Keys(byKey)) {
		files := byKey[key]
		slices.Sort(files)
		if !slices.ContainsFunc(files, func(f string) bool { return !analyzer.Config(f) }) {
			aside[analyzer.Tooling] = append(aside[analyzer.Tooling], files...)
			continue
		}
		u := Unit{Key: key, Kind: kind[key], Files: files, Code: choices(files), Weight: Weigh(files, lines, true), Raw: []string{key}}
		u.Cap = capOf(u, declared)
		code = append(code, u)
	}
	for _, k := range Asides {
		if files := aside[k]; len(files) > 0 {
			slices.Sort(files)
			asides = append(asides, Unit{Key: k, Kind: k, Files: files, Code: files, Weight: Weigh(files, lines, false), Raw: []string{k}})
		}
	}
	return code, asides
}

// choices are the files of files the model picks from: the code, else all.
func choices(files []string) []string {
	if code := slices.DeleteFunc(slices.Clone(files), func(f string) bool { return !Code(f) }); len(code) > 0 {
		return code
	}
	return files
}

// capOf is u's cap: min(subjects, 1 + ⌊weight / LinesPerPage⌋), at least 1,
// the subjects being the distinct names its files declare.
func capOf(u Unit, declared map[string][]string) int {
	names := map[string]bool{}
	for _, f := range u.Files {
		for _, n := range declared[f] {
			names[n] = true
		}
	}
	return max(1, min(len(names), 1+int(u.Weight)/LinesPerPage))
}

// mergeThin merges each thin unit, the lightest first, into its partner
// (partner, needing a shared import or kin) until none merges.
func mergeThin(units []Unit, edges []graphstore.Edge) []Unit {
	for {
		m, root := links(units, edges), shared(units)
		i := slices.IndexFunc(byWeight(units), func(i int) bool {
			return units[i].Weight < LinesPerPage && partner(units, m, root, i, true) >= 0
		})
		if i < 0 {
			return units
		}
		i = byWeight(units)[i]
		units = merge(units, i, partner(units, m, root, i, true))
	}
}

// links counts the import edges between each two units, either way.
func links(units []Unit, edges []graphstore.Edge) [][]int {
	owner := map[string]int{}
	for i, u := range units {
		for _, f := range u.Files {
			owner[f] = i
		}
	}
	m := make([][]int, len(units))
	for i := range m {
		m[i] = make([]int, len(units))
	}
	for _, e := range edges {
		a, ok := owner[e.Src]
		b, ok2 := owner[e.Dst]
		if ok && ok2 && a != b {
			m[a][b]++
			m[b][a]++
		}
	}
	return m
}

// byWeight is units' indexes, the lightest first, ties by key.
func byWeight(units []Unit) []int {
	order := make([]int, len(units))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Or(cmp.Compare(units[a].Weight, units[b].Weight), strings.Compare(units[a].Key, units[b].Key))
	})
	return order
}

// partner is the unit i merges into, by m's import counts and the root
// folders kin leaves out: the most imports shared with it, then the most kin,
// then the lightest; -1 when there is none, or, when linked, none it shares
// an import or kin with.
func partner(units []Unit, m [][]int, root, i int, linked bool) int {
	best, bestE, bestK := -1, 0, 0
	for j := range units {
		if j == i {
			continue
		}
		e, k := m[i][j], kin(units[i], units[j], root)
		if linked && e == 0 && k == 0 {
			continue
		}
		if best < 0 || e > bestE || e == bestE && (k > bestK || k == bestK && units[j].Weight < units[best].Weight) {
			best, bestE, bestK = j, e, k
		}
	}
	return best
}

// merge joins unit i into unit j as one unit, its key both keys sorted and
// joined by ", ".
func merge(units []Unit, i, j int) []Unit {
	u := units[j]
	u.Files = slices.Sorted(slices.Values(append(slices.Clone(u.Files), units[i].Files...)))
	u.Raw = slices.Sorted(slices.Values(append(slices.Clone(u.Raw), units[i].Raw...)))
	u.Key = strings.Join(u.Raw, ", ")
	u.Code = choices(u.Files)
	u.Weight += units[i].Weight
	if u.Kind != units[i].Kind {
		u.Kind = Dir
	}
	units[j] = u
	return slices.Delete(units, i, i+1)
}

// folders is unit u's folders: those every one of its merged keys shares.
func folders(u Unit) []string {
	var out []string
	for i, k := range u.Raw {
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(k, "./"), "/"), "/")
		if k == "./" {
			parts = nil
		}
		if i == 0 {
			out = parts
		} else {
			out = out[:common(out, parts)]
		}
	}
	return out
}

// common is how many leading parts a and b share.
func common(a, b []string) int {
	n := 0
	for n < min(len(a), len(b)) && a[n] == b[n] {
		n++
	}
	return n
}

// shared is how many leading folders every code unit shares: kin counts
// none of them (petclinic's src/main/).
func shared(units []Unit) int {
	if len(units) < 2 {
		return 0
	}
	all := folders(units[0])
	n := len(all)
	for _, u := range units[1:] {
		n = min(n, common(all, folders(u)))
	}
	return n
}

// kin is how many leading folders units a and b share past the root ones.
func kin(a, b Unit, root int) int { return max(0, common(folders(a), folders(b))-root) }
