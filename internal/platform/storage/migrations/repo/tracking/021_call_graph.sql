-- The names each file calls, read once, and a snapshot's name-based call
-- edges: a call to a name declared once in the snapshot, at its line.
ALTER TABLE file_contents ADD COLUMN calls TEXT;

CREATE TABLE call_edges (
	snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
	src TEXT NOT NULL,
	dst TEXT NOT NULL,
	name TEXT NOT NULL,
	src_line INTEGER NOT NULL,
	dst_line INTEGER NOT NULL,
	PRIMARY KEY (snapshot_id, src, dst, name, src_line)
) WITHOUT ROWID;

CREATE INDEX call_edges_dst ON call_edges(snapshot_id, dst);
CREATE INDEX call_edges_name ON call_edges(snapshot_id, name COLLATE NOCASE);
