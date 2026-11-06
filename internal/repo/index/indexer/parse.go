package indexer

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/git"
	"deepcode/internal/repo/index/analyzer"
	"deepcode/internal/repo/index/chunker"
)

const (
	fileBatch  = 100
	batchBytes = 8 << 20 // a batch's text, symbols and chunks are held at once
	maxLineLen = 4000    // longer lines mean minified or generated code
)

type file struct {
	path, sha, lang string
	size            int64
}

type parsedFile struct {
	file
	content string
	lines   int
	symbols []analyzer.Symbol
	chunks  []chunker.Chunk
}

// parse stores the commit's indexable files batch by batch: files already in
// this snapshot are skipped, files unchanged since prev are copied, the rest
// are read from git and analyzed.
func (ix *Indexer) parse(ctx context.Context, dir, sha string, snap, prev int64, progress jobs.Progress) error {
	progress.Stage("parse", 0, 0)
	entries, err := ix.Git.LsTree(ctx, dir, sha)
	if err != nil {
		return err
	}
	files, err := ix.indexable(entries)
	if err != nil {
		return err
	}
	prevSHA, err := pathMap(ctx, ix.DB, `SELECT path, content_hash FROM source_files WHERE snapshot_id = ?`, prev)
	if err != nil {
		return err
	}
	done, err := pathMap(ctx, ix.DB, `SELECT path, '' FROM source_files WHERE snapshot_id = ?`, snap)
	if err != nil {
		return err
	}
	blobs, err := ix.Git.OpenBlobs(ctx, dir)
	if err != nil {
		return err
	}
	defer blobs.Close()
	parser := analyzer.NewParser()
	defer parser.Close()

	progress.Logf("%d of %d files are indexable", len(files), len(entries))
	var parsedN, copiedN, skippedN int
	for i := 0; i < len(files); {
		batch := nextBatch(files, i)
		var parsed []parsedFile
		var copies []string
		for _, f := range batch {
			if _, ok := done[f.path]; ok {
				continue
			}
			if prevSHA[f.path] == f.sha {
				copies = append(copies, f.path)
				continue
			}
			raw, err := blobs.Read(f.sha)
			if err != nil {
				return err
			}
			if p, ok := parseFile(parser, f, raw); ok {
				parsed = append(parsed, p)
			}
		}
		if err := ix.write(ctx, snap, prev, parsed, copies); err != nil {
			return err
		}
		parsedN, copiedN, skippedN = parsedN+len(parsed), copiedN+len(copies), skippedN+len(batch)-len(parsed)-len(copies)
		i += len(batch)
		progress.Stage("parse", i, len(files))
	}
	progress.Logf("parsed %d files, copied %d unchanged since the last snapshot, skipped %d (stored before, binary or minified)", parsedN, copiedN, skippedN)
	return nil
}

// nextBatch is the batch starting at files[i]: at most fileBatch files, and
// batchBytes past the first, which it always holds.
func nextBatch(files []file, i int) []file {
	n, size := 1, files[i].size
	for i+n < len(files) && n < fileBatch && size+files[i+n].size <= batchBytes {
		size += files[i+n].size
		n++
	}
	return files[i : i+n]
}

// indexable keeps the files worth indexing and enforces the size limits.
func (ix *Indexer) indexable(entries []git.TreeEntry) ([]file, error) {
	var files []file
	var total int64
	for _, e := range entries {
		lang, ok := analyzer.Detect(e.Path)
		if !ok || e.Size == 0 || e.Size > ix.Limits.MaxFileBytes || strings.ContainsFunc(e.Path, hidden) {
			continue
		}
		files = append(files, file{path: e.Path, sha: e.SHA, lang: lang, size: e.Size})
		total += e.Size
	}
	if len(files) > ix.Limits.MaxFiles {
		return nil, jobs.Permanent(fmt.Errorf("%d indexable files exceeds the limit of %d (DEEPCODE_MAX_FILES)", len(files), ix.Limits.MaxFiles))
	}
	if total > ix.Limits.MaxTotalBytes {
		return nil, jobs.Permanent(fmt.Errorf("%d MB of indexable text exceeds the limit of %d MB (DEEPCODE_MAX_INDEX_MB)", total>>20, ix.Limits.MaxTotalBytes>>20))
	}
	return files, nil
}

// hidden reports a rune that keeps a path from reading as it is: a control or
// format character, such as the bidirectional controls that make
// "evil\u202egnp.go" read as a picture's name ("Trojan Source"), or a byte
// that is not UTF-8 (read as U+FFFD), which JSON would send as U+FFFD too, so
// two such paths would look alike and neither would open. A path holding one
// is not indexed, so no tree, citation or prompt shows it.
func hidden(r rune) bool { return r == utf8.RuneError || unicode.In(r, unicode.Cc, unicode.Cf) }

// parseFile normalises a blob to UTF-8 text with LF endings and extracts its
// symbols and chunks; binary and minified files are rejected.
func parseFile(p *analyzer.Parser, f file, raw []byte) (parsedFile, bool) {
	if bytes.IndexByte(raw, 0) >= 0 {
		return parsedFile{}, false // binary, wherever the NUL is: files are at most MaxFileBytes
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !utf8.ValidString(content) {
		content = strings.ToValidUTF8(content, "�")
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	for _, l := range lines {
		if len(l) > maxLineLen {
			return parsedFile{}, false
		}
	}
	syms := p.Symbols(f.lang, []byte(content))
	return parsedFile{file: f, content: content, lines: len(lines), symbols: syms, chunks: chunker.Chunks(lines, syms)}, true
}

// pathMap runs a two-column query (path, value) for one snapshot; snapshot 0
// (none) yields an empty map.
func pathMap(ctx context.Context, db *sql.DB, query string, snap int64) (map[string]string, error) {
	m := map[string]string{}
	if snap == 0 {
		return m, nil
	}
	rows, err := db.QueryContext(ctx, query, snap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}
