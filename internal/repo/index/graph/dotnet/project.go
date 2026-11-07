package dotnet

import (
	"encoding/xml"
	"io"
	"path"
	"strings"
)

// project is what a project file, or a Directory.Build.props, says about the
// build: the projects it references and the namespaces every file of it uses.
type project struct {
	refs   []string // ProjectReference, as the file spells them (..\Core\Core.csproj)
	usings []string // <Using Include="X.Y" />, a global using
}

// readProject reads a project file, SDK-style or the old ToolsVersion kind:
// what precedes an XML error, if any.
func readProject(content string) project {
	d := xml.NewDecoder(strings.NewReader(content)) // a BOM before it is only character data
	d.Strict = false
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil } // content is already text
	var p project
	for {
		tok, err := d.Token()
		if err != nil {
			return p
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch el.Name.Local {
		case "ProjectReference":
			p.refs = append(p.refs, items(el)...)
		case "Using": // Static and Alias ones name a type, not a namespace
			if attr(el, "Static") == "" && attr(el, "Alias") == "" {
				p.usings = append(p.usings, items(el)...)
			}
		}
	}
}

// items are an item's Include values: MSBuild separates several with ;.
func items(el xml.StartElement) []string {
	var out []string
	for _, v := range strings.Split(attr(el, "Include"), ";") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// attr is the value of el's attribute name, "" when it has none.
func attr(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// hasSources reports whether p is a project file whose directory holds C# or
// VB.NET files; an F# project only references others.
func hasSources(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".csproj" || ext == ".vbproj"
}

// projects reads every project file of configs, F# ones too, with the global
// usings of the nearest Directory.Build.props at or above it, as MSBuild
// imports only that one.
func projects(configs map[string]string) map[string]project {
	props := map[string][]string{} // directory → its Directory.Build.props' usings
	for p, content := range configs {
		if strings.EqualFold(path.Base(p), "Directory.Build.props") {
			props[dir(p)] = readProject(content).usings
		}
	}
	out := map[string]project{}
	for p, content := range configs {
		if !hasSources(p) && strings.ToLower(path.Ext(p)) != ".fsproj" {
			continue
		}
		pr := readProject(content)
		u, _ := nearest(props, dir(p))
		pr.usings = append(pr.usings, u...)
		out[p] = pr
	}
	return out
}

// references are the edges between project files: each ProjectReference
// relative to its project's directory, with backslashes, matched without
// regard to case as Windows builds do.
func references(projs map[string]project, configs map[string]string) [][2]string {
	lower := map[string]string{}
	for p := range configs {
		lower[strings.ToLower(p)] = p
	}
	var out [][2]string
	for p, pr := range projs {
		for _, r := range pr.refs {
			dst := path.Join(dir(p), strings.ReplaceAll(r, `\`, "/"))
			if _, ok := configs[dst]; !ok {
				dst = lower[strings.ToLower(dst)]
			}
			if dst != "" && dst != p {
				out = append(out, [2]string{p, dst})
			}
		}
	}
	return out
}

// nearest is the value of byDir at directory d or the closest directory
// above it, up to the root.
func nearest[V any](byDir map[string]V, d string) (V, bool) {
	for {
		if v, ok := byDir[d]; ok || d == "" {
			return v, ok
		}
		d = dir(d)
	}
}

// dir is the directory of a repository path, "" at the root.
func dir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}
