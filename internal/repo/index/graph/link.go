// Package graph links a snapshot's files by their imports, Go's through
// go.mod and JavaScript's and TypeScript's through relative paths, and
// answers what retrieval and the wiki ask of those links.
package graph

import (
	"context"
	"strings"

	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/graph/store"
)

// Graph is the import graph of the snapshots, kept in Store.
type Graph struct{ Store *store.Store }

// How many files' imports Link reads, and how many edges it stores or
// deletes, in one transaction.
const (
	readBatch  = 200
	storeBatch = 5000
)

// Imports is how a file's imports are stored: a line each.
func Imports(specs []string) string { return strings.Join(specs, "\n") }

// Link reads the imports the snapshot's files lack, which an older version
// stored without, then rebuilds the snapshot's edges. It returns how many
// edges it stored; a pruned snapshot gets none.
func (g *Graph) Link(ctx context.Context, snap int64) (int, error) {
	if err := g.readImports(ctx, snap); err != nil {
		return 0, err
	}
	files, configs, err := g.load(ctx, snap)
	if err != nil {
		return 0, err
	}
	return g.save(ctx, snap, resolve(files, configs))
}

// LinkStale links the ready snapshots that have files with a grammar but no
// edges: those published before this version, or by an older one run since.
// One whose files import none of each other is linked again at every start,
// which costs a read of its imports. It returns how many it linked.
func (g *Graph) LinkStale(ctx context.Context) (int, error) {
	ids, err := g.Store.Stale(ctx, analyzer.Grammars())
	if err != nil {
		return 0, err
	}
	for i, id := range ids {
		if _, err := g.Link(ctx, id); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}

// readImports parses the files of the snapshot whose imports were never read,
// readBatch at a time, so no transaction holds the write lock for long.
func (g *Graph) readImports(ctx context.Context, snap int64) error {
	p := analyzer.NewParser()
	defer p.Close()
	langs := analyzer.Grammars()
	for last := ""; ; {
		var batch []store.Imports
		err := g.Store.Unread(ctx, snap, langs, last, readBatch, func(hash, lang, content string) {
			batch = append(batch, store.Imports{Hash: hash, Imports: Imports(p.Parse(lang, []byte(content)).Imports)})
		})
		if err != nil || len(batch) == 0 {
			return err
		}
		if err := g.Store.SetImports(ctx, batch); err != nil {
			return err
		}
		last = batch[len(batch)-1].Hash
	}
}

// load reads the snapshot's files with a grammar, generated ones aside, with
// their imports, and the content of its configs by path: go.mod, .NET
// project files and Cargo.toml.
func (g *Graph) load(ctx context.Context, snap int64) ([]file, map[string]string, error) {
	var files []file
	err := g.Store.Files(ctx, snap, analyzer.Grammars(), func(path, lang, imports string) {
		f := file{path: path, lang: lang}
		if imports != "" {
			f.imports = strings.Split(imports, "\n")
		}
		files = append(files, f)
	})
	if err != nil {
		return nil, nil, err
	}
	contents, err := g.Store.Configs(ctx, snap)
	return files, contents, err
}

// save replaces the snapshot's edges, storeBatch at a time so no
// transaction holds the write lock for long (a Go monorepo has millions),
// and stops once the snapshot is pruned or gone: prune would not come back
// for the rows. It returns how many it stored.
// ponytail: a crash between batches leaves part of the edges, which LinkStale
// takes for a linked snapshot; the repository's next index replaces them.
func (g *Graph) save(ctx context.Context, snap int64, edges []edge) (int, error) {
	// A served snapshot that has edges, read again after an analyzer upgrade,
	// swaps them in one transaction, so its readers never see half a graph.
	// ponytail: that transaction holds the write lock for all of a monorepo's edges.
	swap, ok, err := g.Store.Swap(ctx, snap)
	if err != nil || !ok {
		return 0, err
	}
	if swap {
		return g.Store.Replace(ctx, snap, edges)
	}
	if err := g.Unlink(ctx, snap); err != nil {
		return 0, err
	}
	for i := 0; i < len(edges); i += storeBatch {
		if pruned, err := g.Store.Insert(ctx, snap, edges[i:min(i+storeBatch, len(edges))]); err != nil || pruned {
			return 0, err
		}
	}
	return len(edges), nil
}

// Unlink deletes the snapshot's edges, storeBatch at a time so no statement
// holds the write lock for long.
func (g *Graph) Unlink(ctx context.Context, snap int64) error {
	return g.Store.Unlink(ctx, snap, storeBatch)
}
