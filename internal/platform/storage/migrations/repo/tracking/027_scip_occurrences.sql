CREATE TABLE scip_occurrences (
    id INTEGER PRIMARY KEY,
    snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    line INTEGER NOT NULL,
    byte_column INTEGER NOT NULL,
    symbol TEXT NOT NULL,
    definition_name TEXT NOT NULL
);
CREATE INDEX scip_snapshot ON scip_occurrences(snapshot_id, path);
CREATE INDEX scip_definitions ON scip_occurrences(snapshot_id, path, line, definition_name, symbol)
    WHERE definition_name <> '';
CREATE INDEX scip_references ON scip_occurrences(snapshot_id, symbol, path, line)
    WHERE definition_name = '';
