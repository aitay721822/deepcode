-- Bearer tokens for /mcp, and the OAuth apps a user lets act for them. Only a
-- secret's SHA-256 is stored: the database alone signs nobody in.

-- An app that registered itself (RFC 7591): public, so it has no secret.
CREATE TABLE oauth_clients (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  redirect_uris TEXT NOT NULL, -- a JSON array
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- A personal access token (client_id NULL), or the access token an app was
-- given (client_id set). expires_at NULL: until revoked.
CREATE TABLE api_tokens (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  client_id TEXT REFERENCES oauth_clients(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_used_at TEXT,
  expires_at TEXT
);
CREATE INDEX api_tokens_user ON api_tokens(user_id, client_id);

-- An authorization code: used once, within minutes, with the PKCE verifier
-- whose S256 is challenge.
CREATE TABLE oauth_codes (
  code_hash TEXT PRIMARY KEY,
  client_id TEXT NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  redirect_uri TEXT NOT NULL,
  challenge TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

-- A refresh token, replaced by a new one each time it is used.
CREATE TABLE oauth_refresh_tokens (
  token_hash TEXT PRIMARY KEY,
  client_id TEXT NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TEXT NOT NULL
);
CREATE INDEX oauth_refresh_tokens_user ON oauth_refresh_tokens(user_id, client_id);
