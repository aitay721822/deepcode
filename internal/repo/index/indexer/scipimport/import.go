// Package scipimport validates isolated SCIP output and trusted Go artifacts.
package scipimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"deepcode/internal/repo/git"
	"deepcode/internal/repo/index/indexer/store"
)

const (
	maxManifest    = 4 << 20
	maxIndex       = 16 << 20
	maxDocuments   = 5000
	maxOccurrences = 200000
	maxSymbol      = 8192
)

type manifest struct {
	Repository string            `json:"repository"`
	Commit     string            `json:"commit"`
	ModuleRoot string            `json:"module_root"`
	IndexSHA   string            `json:"index_sha256"`
	Files      map[string]string `json:"files"`
}

// Import checks an artifact against a ready snapshot and publishes it without
// changing the snapshot's base index. The input must come from a trusted CI
// job: hashes detect inconsistent inputs but cannot attest to their origin.
func Import(ctx context.Context, s *store.Store, gitc *git.Client, manifestFile, indexFile io.Reader) (int, error) {
	raw, err := bounded(manifestFile, maxManifest)
	if err != nil {
		return 0, fmt.Errorf("read SCIP manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, fmt.Errorf("parse SCIP manifest: %w", err)
	}
	if m.Repository == "" || !oid(m.Commit) || len(m.Files) == 0 {
		return 0, errors.New("SCIP manifest requires repository, full commit SHA, and Git blob hashes")
	}
	if m.ModuleRoot != "" && !validPath(m.ModuleRoot) {
		return 0, errors.New("SCIP module_root must be a repository-relative directory")
	}
	snapshot, repoID, err := s.SCIPSnapshot(ctx, m.Repository, m.Commit)
	if err != nil {
		return 0, err
	}
	raw, err = bounded(indexFile, maxIndex)
	if err != nil {
		return 0, fmt.Errorf("read SCIP index: %w", err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != m.IndexSHA {
		return 0, errors.New("SCIP index SHA-256 does not match the manifest")
	}
	var blobs map[string]string
	verifySkipped := func(name string) error {
		if blobs == nil {
			tree, err := gitc.CachedTree(ctx, repoID, m.Commit)
			if err != nil {
				return fmt.Errorf("cannot verify unindexed SCIP documents against the cached commit: %w", err)
			}
			blobs = goBlobs(tree)
		}
		if blobs[name] != m.Files[name] {
			return fmt.Errorf("SCIP document %q does not match a Go blob in the commit", name)
		}
		return nil
	}
	rows, err := readIndex(ctx, s, snapshot, m.ModuleRoot, m.Files, raw, verifySkipped)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, errors.New("SCIP index contains no global symbol occurrences in snapshot files")
	}
	if err := s.ReplaceSCIP(ctx, snapshot, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// readIndex uses the manifest's blob list for external artifacts and the
// pinned Git tree directly for locally generated ones.
func readIndex(ctx context.Context, s *store.Store, snapshot int64, moduleRoot string, files map[string]string, raw []byte, verifySkipped func(string) error) ([]store.SCIPOccurrence, error) {
	return readLanguageIndex(ctx, s, snapshot, moduleRoot, files, raw, verifySkipped, specs[0], nil)
}

func readLanguageIndex(ctx context.Context, s *store.Store, snapshot int64, moduleRoot string, files map[string]string, raw []byte, verifySkipped func(string) error, spec languageSpec, documents map[string]bool) ([]store.SCIPOccurrence, error) {
	var idx scip.Index
	if err := (proto.UnmarshalOptions{DiscardUnknown: true, RecursionLimit: 32}).Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("decode SCIP index: %w", err)
	}
	if idx.GetMetadata().GetToolInfo().GetName() != spec.tool || idx.GetMetadata().GetTextDocumentEncoding() != scip.TextEncoding_UTF8 {
		return nil, fmt.Errorf("SCIP requires UTF-8 indexes from %s", spec.tool)
	}
	if len(idx.Documents) == 0 && !spec.emptyDocuments || len(idx.Documents) > maxDocuments {
		return nil, fmt.Errorf("SCIP index must contain 1–%d documents", maxDocuments)
	}
	paths := make(map[string]bool, len(idx.Documents))
	var rows []store.SCIPOccurrence
	var occurrences int
	symbols := make(map[string]bool)
	for _, doc := range idx.Documents {
		rel := doc.GetRelativePath()
		lang, known := spec.extensions[path.Ext(rel)]
		occurrences += len(doc.Occurrences)
		if occurrences > maxOccurrences {
			return nil, fmt.Errorf("SCIP index exceeds %d occurrences", maxOccurrences)
		}
		if spec.skipParentDocuments && strings.HasPrefix(path.Clean(rel), "../") {
			if spec.rejectParentDocuments {
				return nil, fmt.Errorf("packageless TypeScript/JavaScript project emitted document outside its root: %q", rel)
			}
			continue
		}
		if !known {
			continue
		}
		name := path.Join(moduleRoot, rel)
		if !validPath(rel) || paths[name] || !oid(files[name]) {
			return nil, fmt.Errorf("invalid, duplicate, or unlisted SCIP document path %q", rel)
		}
		paths[name] = true
		if documents != nil {
			documents[name] = true
		}
		label, known := spec.labels[doc.GetLanguage()]
		if !known || label != lang || doc.GetPositionEncoding() != spec.encoding {
			return nil, fmt.Errorf("SCIP document %q has an unsupported %s language or position encoding", name, spec.tool)
		}
		source, indexed, err := s.SCIPSource(ctx, snapshot, name, files[name], spec.languages...)
		if err != nil {
			return nil, err
		}
		if !indexed {
			if verifySkipped != nil {
				if err := verifySkipped(name); err != nil {
					return nil, err
				}
			}
			continue // The commit matches, but there is no base evidence for this file.
		}
		if doc.Text != "" && strings.ReplaceAll(doc.Text, "\r\n", "\n") != source {
			return nil, fmt.Errorf("SCIP document %q text differs from the snapshot", name)
		}
		rows, err = appendDocument(rows, name, doc, strings.Split(source, "\n"), symbols, spec)
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func goBlobs(tree []git.TreeEntry) map[string]string {
	blobs := make(map[string]string)
	for _, entry := range tree {
		if strings.HasSuffix(entry.Path, ".go") {
			blobs[entry.Path] = entry.SHA
		}
	}
	return blobs
}

func appendDocument(rows []store.SCIPOccurrence, name string, doc *scip.Document, lines []string, symbols map[string]bool, spec languageSpec) ([]store.SCIPOccurrence, error) {
	typeSpans := map[scip.Range]bool{}
	if spec.syntheticConstructors {
		for _, occ := range doc.Occurrences {
			if occ.GetSymbolRoles()&int32(scip.SymbolRole_Definition) == 0 {
				continue
			}
			symbol, err := scip.ParseSymbol(occ.GetSymbol())
			if err != nil || len(symbol.Descriptors) == 0 {
				continue
			}
			if symbol.Descriptors[len(symbol.Descriptors)-1].Suffix == scip.Descriptor_Type {
				if r, ok := occ.SourceRange(); ok {
					typeSpans[r] = true
				}
			}
		}
	}
	for _, occ := range doc.Occurrences {
		if occ.GetSymbol() == "" || !scip.IsGlobalSymbol(occ.Symbol) {
			continue
		}
		r, ok := occ.SourceRange()
		if spec.syntheticConstructors && ok && typeSpans[r] && occ.GetSymbolRoles()&int32(scip.SymbolRole_Definition) != 0 {
			symbol, err := scip.ParseSymbol(occ.Symbol)
			if err == nil && len(symbol.Descriptors) > 0 {
				last := symbol.Descriptors[len(symbol.Descriptors)-1]
				if last.Suffix == scip.Descriptor_Method && last.Name == "<init>" {
					continue
				}
			}
		}
		if ok && moduleAnchor(occ, r, spec) {
			continue
		}
		if ok && spec.utf16 {
			r.Start, ok = bytePosition(lines, r.Start)
			if ok {
				r.End, ok = bytePosition(lines, r.End)
			}
		}
		if !ok || !validPosition(lines, r.Start) || !validPosition(lines, r.End) ||
			r.End.Line < r.Start.Line || r.End.Line == r.Start.Line && r.End.Character <= r.Start.Character {
			return nil, fmt.Errorf("invalid SCIP source range in %q", name)
		}
		if !symbols[occ.Symbol] {
			if len(occ.Symbol) > maxSymbol {
				return nil, fmt.Errorf("oversized SCIP symbol in %q", name)
			}
			if _, err := scip.ParseSymbol(occ.Symbol); err != nil {
				return nil, fmt.Errorf("invalid SCIP symbol in %q: %w", name, err)
			}
			symbols[occ.Symbol] = true
		}
		row := store.SCIPOccurrence{Path: name, Line: int(r.Start.Line) + 1, ByteColumn: int(r.Start.Character), Symbol: occ.Symbol}
		if occ.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 {
			if r.Start.Line != r.End.Line {
				continue // The pilot selects declarations by the identifier token.
			}
			row.DefinitionName = lines[r.Start.Line][r.Start.Character:r.End.Character]
			if !spec.identifier(row.DefinitionName) {
				continue
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func moduleAnchor(occ *scip.Occurrence, r scip.Range, spec languageSpec) bool {
	if !spec.anchors || occ.SymbolRoles != int32(scip.SymbolRole_Definition) || r.Start.Line != 0 || r.Start.Character != 0 || r.End.Line != 0 || r.End.Character != 0 {
		return false
	}
	if len(occ.Symbol) > maxSymbol {
		return false
	}
	symbol, err := scip.ParseSymbol(occ.Symbol)
	if err != nil || len(symbol.Descriptors) == 0 {
		return false
	}
	last := symbol.Descriptors[len(symbol.Descriptors)-1]
	if last.Suffix != spec.anchorSuffix || spec.anchorName != "" && last.Name != spec.anchorName {
		return false
	}
	for _, d := range symbol.Descriptors[:len(symbol.Descriptors)-1] {
		if d.Suffix != scip.Descriptor_Namespace {
			return false
		}
	}
	return true
}

func bytePosition(lines []string, p scip.Position) (scip.Position, bool) {
	if p.Line < 0 || int(p.Line) >= len(lines) || p.Character < 0 {
		return p, false
	}
	line := lines[p.Line]
	var units int32
	for offset, r := range line {
		if units == p.Character {
			p.Character = int32(offset)
			return p, true
		}
		units++
		if r > 0xffff {
			units++
		}
		if units > p.Character {
			return p, false
		}
	}
	if units == p.Character {
		p.Character = int32(len(line))
		return p, true
	}
	return p, false
}

func validPath(p string) bool {
	return p != "." && fs.ValidPath(p) && !strings.ContainsAny(p, "\\:") &&
		!strings.ContainsFunc(p, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
}

func validPosition(lines []string, p scip.Position) bool {
	line, col := int(p.Line), int(p.Character)
	if line < 0 || line >= len(lines) {
		return false
	}
	s := lines[line]
	return col >= 0 && col <= len(s) && (col == len(s) || utf8.RuneStart(s[col]))
}

func oid(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func bounded(r io.Reader, maxBytes int64) ([]byte, error) {
	var b bytes.Buffer
	if _, err := b.ReadFrom(io.LimitReader(r, maxBytes+1)); err != nil {
		return nil, err
	}
	if int64(b.Len()) > maxBytes {
		return nil, fmt.Errorf("exceeds %d bytes", maxBytes)
	}
	return b.Bytes(), nil
}
