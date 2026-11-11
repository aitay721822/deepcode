-- The sealed secret that authenticates a repository's webhook; empty is off.
ALTER TABLE repositories ADD COLUMN webhook_secret TEXT NOT NULL DEFAULT '';
