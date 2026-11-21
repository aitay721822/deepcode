// Package scipimport validates and imports Go SCIP artifacts from trusted builds.
package scipimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"io"
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

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
	IndexSHA   string            `json:"index_sha256"`
	Files      map[string]string `json:"files"`
}

// Import checks an artifact against a ready snapshot and publishes it without
// changing the snapshot's base index. The input must come from a trusted CI
// job: hashes detect inconsistent inputs but cannot attest to their origin.
func Import(ctx context.Context, s *store.Store, manifestFile, indexFile io.Reader) (int, error) {
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
	snapshot, err := s.SCIPSnapshot(ctx, m.Repository, m.Commit)
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
	var idx scip.Index
	if err := (proto.UnmarshalOptions{DiscardUnknown: true, RecursionLimit: 32}).Unmarshal(raw, &idx); err != nil {
		return 0, fmt.Errorf("decode SCIP index: %w", err)
	}
	if idx.GetMetadata().GetToolInfo().GetName() != "scip-go" || idx.GetMetadata().GetTextDocumentEncoding() != scip.TextEncoding_UTF8 {
		return 0, errors.New("SCIP pilot accepts UTF-8 indexes from scip-go only")
	}
	if len(idx.Documents) == 0 || len(idx.Documents) > maxDocuments {
		return 0, fmt.Errorf("SCIP index must contain 1–%d documents", maxDocuments)
	}
	paths := make(map[string]bool, len(idx.Documents))
	var rows []store.SCIPOccurrence
	var occurrences int
	symbols := make(map[string]bool)
	for _, doc := range idx.Documents {
		path := doc.GetRelativePath()
		if !validPath(path) || paths[path] || !oid(m.Files[path]) {
			return 0, fmt.Errorf("invalid, duplicate, or unlisted SCIP document path %q", path)
		}
		paths[path] = true
		occurrences += len(doc.Occurrences)
		if occurrences > maxOccurrences {
			return 0, fmt.Errorf("SCIP index exceeds %d occurrences", maxOccurrences)
		}
		if doc.GetLanguage() != "go" || doc.GetPositionEncoding() != scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart {
			return 0, fmt.Errorf("SCIP document %q must use Go and UTF-8 byte offsets", path)
		}
		source, err := s.SCIPSource(ctx, snapshot, path, m.Files[path])
		if err != nil {
			return 0, err
		}
		if doc.Text != "" && strings.ReplaceAll(doc.Text, "\r\n", "\n") != source {
			return 0, fmt.Errorf("SCIP document %q text differs from the snapshot", path)
		}
		rows, err = appendDocument(rows, doc, strings.Split(source, "\n"), symbols)
		if err != nil {
			return 0, err
		}
	}
	if len(rows) == 0 {
		return 0, errors.New("SCIP index contains no global symbol occurrences")
	}
	if err := s.ReplaceSCIP(ctx, snapshot, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

func appendDocument(rows []store.SCIPOccurrence, doc *scip.Document, lines []string, symbols map[string]bool) ([]store.SCIPOccurrence, error) {
	for _, occ := range doc.Occurrences {
		if occ.GetSymbol() == "" || !scip.IsGlobalSymbol(occ.Symbol) {
			continue
		}
		r, ok := occ.SourceRange()
		if !ok || !validPosition(lines, r.Start) || !validPosition(lines, r.End) ||
			r.End.Line < r.Start.Line || r.End.Line == r.Start.Line && r.End.Character <= r.Start.Character {
			return nil, fmt.Errorf("invalid SCIP source range in %q", doc.RelativePath)
		}
		if !symbols[occ.Symbol] {
			if len(occ.Symbol) > maxSymbol {
				return nil, fmt.Errorf("oversized SCIP symbol in %q", doc.RelativePath)
			}
			if _, err := scip.ParseSymbol(occ.Symbol); err != nil {
				return nil, fmt.Errorf("invalid SCIP symbol in %q: %w", doc.RelativePath, err)
			}
			symbols[occ.Symbol] = true
		}
		row := store.SCIPOccurrence{Path: doc.RelativePath, Line: int(r.Start.Line) + 1, ByteColumn: int(r.Start.Character), Symbol: occ.Symbol}
		if occ.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 {
			if r.Start.Line != r.End.Line {
				continue // The pilot selects declarations by the identifier token.
			}
			row.DefinitionName = lines[r.Start.Line][r.Start.Character:r.End.Character]
			if !token.IsIdentifier(row.DefinitionName) {
				continue
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func validPath(p string) bool {
	return p != "." && fs.ValidPath(p) && strings.HasSuffix(p, ".go") && !strings.ContainsAny(p, "\\:") &&
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
