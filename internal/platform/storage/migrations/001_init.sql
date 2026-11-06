CREATE TABLE repositories (
  id INTEGER PRIMARY KEY,
  url TEXT NOT NULL UNIQUE,
  owner TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  stars INTEGER NOT NULL DEFAULT 0,
  default_branch TEXT NOT NULL DEFAULT '',
  ready_snapshot_id INTEGER REFERENCES snapshots(id),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- status: indexing | ready | failed | pruned. wiki_status: none | generating | ready | failed
CREATE TABLE snapshots (
  id INTEGER PRIMARY KEY,
  repository_id INTEGER NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  commit_sha TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'indexing',
  wiki_status TEXT NOT NULL DEFAULT 'none',
  embedding_model TEXT NOT NULL DEFAULT '',
  file_count INTEGER NOT NULL DEFAULT 0,
  chunk_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  indexed_at TEXT,
  UNIQUE (repository_id, commit_sha)
);

-- Keyed by git blob SHA, so unchanged files share one row across snapshots.
CREATE TABLE file_contents (
  hash TEXT PRIMARY KEY,
  content TEXT NOT NULL
);

CREATE TABLE source_files (
  id INTEGER PRIMARY KEY,
  snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  language TEXT NOT NULL,
  content_hash TEXT NOT NULL REFERENCES file_contents(hash),
  size INTEGER NOT NULL,
  line_count INTEGER NOT NULL,
  UNIQUE (snapshot_id, path)
);
CREATE INDEX source_files_hash ON source_files(content_hash);

CREATE TABLE symbols (
  id INTEGER PRIMARY KEY,
  snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  name TEXT NOT NULL,
  qualified_name TEXT NOT NULL,
  kind TEXT NOT NULL,
  start_line INTEGER NOT NULL,
  end_line INTEGER NOT NULL,
  signature TEXT NOT NULL DEFAULT ''
);
CREATE INDEX symbols_name ON symbols(snapshot_id, name COLLATE NOCASE);
CREATE INDEX symbols_path ON symbols(snapshot_id, path);

-- content_hash covers the exact embedding input, so equal hashes can share a vector.
CREATE TABLE chunks (
  id INTEGER PRIMARY KEY,
  snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  symbol_name TEXT NOT NULL DEFAULT '',
  start_line INTEGER NOT NULL,
  end_line INTEGER NOT NULL,
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL
);
CREATE INDEX chunks_path ON chunks(snapshot_id, path);
CREATE INDEX chunks_hash ON chunks(content_hash);

CREATE VIRTUAL TABLE chunk_fts USING fts5(
  path, symbol_name, content,
  content='chunks', content_rowid='id'
);

CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
  INSERT INTO chunk_fts(rowid, path, symbol_name, content)
  VALUES (new.id, new.path, new.symbol_name, new.content);
END;

CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
  INSERT INTO chunk_fts(chunk_fts, rowid, path, symbol_name, content)
  VALUES ('delete', old.id, old.path, old.symbol_name, old.content);
END;

CREATE TRIGGER chunks_au AFTER UPDATE ON chunks BEGIN
  INSERT INTO chunk_fts(chunk_fts, rowid, path, symbol_name, content)
  VALUES ('delete', old.id, old.path, old.symbol_name, old.content);
  INSERT INTO chunk_fts(rowid, path, symbol_name, content)
  VALUES (new.id, new.path, new.symbol_name, new.content);
END;

-- Vectors are float32 little-endian, L2-normalised before storage.
CREATE TABLE chunk_embeddings (
  chunk_id INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
  model TEXT NOT NULL,
  dimension INTEGER NOT NULL,
  embedding BLOB NOT NULL,
  PRIMARY KEY (chunk_id, model)
);

-- Pages are inserted with empty content when planned and filled one by one.
CREATE TABLE wiki_pages (
  id INTEGER PRIMARY KEY,
  snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  title TEXT NOT NULL,
  query TEXT NOT NULL DEFAULT '',
  position INTEGER NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (snapshot_id, slug)
);

CREATE TABLE wiki_citations (
  page_id INTEGER NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  start_line INTEGER NOT NULL,
  end_line INTEGER NOT NULL
);
CREATE INDEX wiki_citations_page ON wiki_citations(page_id);

-- status: queued | running | succeeded | failed
CREATE TABLE jobs (
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,
  repository_id INTEGER NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  commit_sha TEXT NOT NULL,
  idempotency_key TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'queued',
  attempts INTEGER NOT NULL DEFAULT 0,
  stage TEXT NOT NULL DEFAULT '',
  progress INTEGER NOT NULL DEFAULT 0,
  total INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  run_after TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX jobs_queue ON jobs(status, run_after);
CREATE INDEX jobs_repository ON jobs(repository_id, id);

CREATE TABLE conversations (
  id INTEGER PRIMARY KEY,
  repository_id INTEGER NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  snapshot_id INTEGER NOT NULL REFERENCES snapshots(id),
  title TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE messages (
  id INTEGER PRIMARY KEY,
  conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  citations TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX messages_conversation ON messages(conversation_id, id);
