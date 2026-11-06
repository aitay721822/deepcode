-- Stem words so a question's "subscribers" or "notifies" also finds subscribe
-- and notify in code. The chunks_* triggers name chunk_fts and keep working;
-- the index is rebuilt from chunks in this migration's transaction.
DROP TABLE chunk_fts;
CREATE VIRTUAL TABLE chunk_fts USING fts5(
  path, symbol_name, content,
  content='chunks', content_rowid='id',
  tokenize='porter unicode61'
);
INSERT INTO chunk_fts(chunk_fts) VALUES ('rebuild');
