-- The import graph. A file's imports go with its content, as its source
-- spells them, one per line: '' when it has none, NULL until read. Only files
-- in a language with a grammar are read; those stored before this version are
-- read when a snapshot that holds them is linked again, and the snapshots
-- already published are linked in the background at start.
ALTER TABLE file_contents ADD COLUMN imports TEXT;

-- A snapshot's imports resolved to its own files: src imports dst. Rebuilt
-- whole whenever the snapshot is linked. Read by snapshot, or by snapshot
-- and src: the primary key serves both.
CREATE TABLE file_edges (
  snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  src TEXT NOT NULL,
  dst TEXT NOT NULL,
  PRIMARY KEY (snapshot_id, src, dst)
) WITHOUT ROWID;
