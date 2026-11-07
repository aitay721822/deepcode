-- The words of a chunk's compound identifiers (getUserToken: get user token),
-- which unicode61 keeps whole: with them a question that says "token" finds
-- getUserToken. SQL cannot split camelCase, so the column stays NULL until
-- deepcode fills it in: new chunks as they are stored, those stored before
-- this migration in the background after the next start. A full-text table
-- takes no new column, so chunk_fts and its triggers are made again, and the
-- index is rebuilt from chunks in this migration's transaction.
ALTER TABLE chunks ADD COLUMN terms TEXT;

DROP TRIGGER chunks_ai;
DROP TRIGGER chunks_ad;
DROP TRIGGER chunks_au;
DROP TABLE chunk_fts;

CREATE VIRTUAL TABLE chunk_fts USING fts5(
  path, symbol_name, content, terms,
  content='chunks', content_rowid='id',
  tokenize='porter unicode61'
);

CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
  INSERT INTO chunk_fts(rowid, path, symbol_name, content, terms)
  VALUES (new.id, new.path, new.symbol_name, new.content, new.terms);
END;

CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
  INSERT INTO chunk_fts(chunk_fts, rowid, path, symbol_name, content, terms)
  VALUES ('delete', old.id, old.path, old.symbol_name, old.content, old.terms);
END;

CREATE TRIGGER chunks_au AFTER UPDATE ON chunks BEGIN
  INSERT INTO chunk_fts(chunk_fts, rowid, path, symbol_name, content, terms)
  VALUES ('delete', old.id, old.path, old.symbol_name, old.content, old.terms);
  INSERT INTO chunk_fts(rowid, path, symbol_name, content, terms)
  VALUES (new.id, new.path, new.symbol_name, new.content, new.terms);
END;

INSERT INTO chunk_fts(chunk_fts) VALUES ('rebuild');
