// The snapshot's rows: batches written while parsing, the terms an older
// version left out filled in, and the data of older snapshots pruned once a
// newer one is published.

package indexer

import (
	"context"

	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/graph"
	"deepcode/internal/repo/index/indexer/chunker"
	"deepcode/internal/repo/index/indexer/store"
)

// termsBatch is how many chunks FillTerms splits in one transaction.
const termsBatch = 500

// write commits one batch in a short transaction: a file's rows land together,
// which is what makes an interrupted run resumable.
func (ix *Indexer) write(ctx context.Context, snap, prev int64, parsed []parsedFile, copies []string) error {
	files := make([]store.File, len(parsed))
	for i, p := range parsed {
		var imports, references *string                    // NULL: Parse reads no imports or calls of the language, or the file is generated
		if analyzer.ReadsImports(p.lang) && !p.generated { // a hand-written copy fills them in
			s := graph.Imports(p.imports)
			imports = &s
		}
		if analyzer.HasGrammar(p.lang) && !p.generated {
			s := graph.Imports(p.references)
			references = &s
		}
		f := store.File{Path: p.path, Lang: p.lang, Hash: p.sha, Content: p.content, Imports: imports, References: references,
			Size: p.size, Lines: p.lines, Generated: p.generated, Symbols: symbols(nil, p.symbols, "")}
		for _, c := range p.chunks {
			f.Chunks = append(f.Chunks, store.Chunk{Symbol: c.Symbol, StartLine: c.StartLine, EndLine: c.EndLine, Content: c.Content,
				Hash: contentHash(p.path, c.Symbol, c.Content), Terms: chunker.Terms(p.path, c.Content)})
		}
		files[i] = f
	}
	return ix.Store.Write(ctx, snap, prev, files, copies)
}

// symbols appends syms and their children's, named under parent, in the
// order they are stored.
func symbols(out []store.Symbol, syms []analyzer.Symbol, parent string) []store.Symbol {
	for _, s := range syms {
		q := s.QualifiedName(parent)
		out = append(out, store.Symbol{Name: s.Name, QualifiedName: q, Kind: s.Kind, StartLine: s.StartLine, EndLine: s.EndLine, Signature: s.Signature})
		out = symbols(out, s.Children, q)
	}
	return out
}

// FillTerms gives the chunks an older version stored, whose terms are NULL,
// the words of their compound identifiers, termsBatch at a time in id order
// so no transaction holds the write lock for long. Until then such a chunk is
// found by its content alone, as before; a stop leaves the rest to the next
// start. It returns how many chunks it filled.
func (ix *Indexer) FillTerms(ctx context.Context) (int, error) {
	filled := 0
	for last := int64(0); ; {
		var batch []store.Terms
		err := ix.Store.Untermed(ctx, last, termsBatch, func(id int64, path, content, hash string) {
			batch = append(batch, store.Terms{ID: id, Hash: hash, Terms: chunker.Terms(path, content)})
		})
		if err != nil || len(batch) == 0 {
			return filled, err
		}
		// Written only to chunks that still hold what was read: one deleted
		// meanwhile and stored again at its id is left for the next pass.
		set, err := ix.Store.SetTerms(ctx, batch)
		if err != nil {
			return filled, err
		}
		filled, last = filled+set, batch[len(batch)-1].ID
	}
}

// prune keeps the published snapshot and the one before it; older snapshots
// keep their row (conversations point at it) and their wiki, but lose their
// indexed data. A snapshot left half emptied, by a stop midway, is finished
// here too, and so are file contents no snapshot uses any more.
func (ix *Indexer) prune(ctx context.Context, repoID, current int64) error {
	stale, err := ix.Store.Stale(ctx, repoID, current)
	if err != nil {
		return err
	}
	for _, id := range stale {
		if err := ix.dropSnapshotData(ctx, id); err != nil {
			return err
		}
	}
	return ix.Store.DropUnusedContents(ctx)
}

// dropSnapshotData marks a snapshot pruned, so readers leave it at once,
// then deletes its edges and its index rows (vectors follow chunks by
// cascade); source files go last, which is how prune tells a snapshot that
// still holds data.
func (ix *Indexer) dropSnapshotData(ctx context.Context, id int64) error {
	if err := ix.Store.MarkPruned(ctx, id); err != nil {
		return err
	}
	if err := ix.Graph.Unlink(ctx, id); err != nil {
		return err
	}
	return ix.Store.DropRows(ctx, id)
}
