-- A git server can be authorized with OAuth instead of a pasted token: the
-- OAuth app the admin registered on it (oauth_client_secret sealed like
-- token, '' for none), and what an authorization leaves besides the access
-- token kept in token: the refresh token (sealed), the redirect URI it was
-- granted for, which a GitLab refresh repeats, and when the access token
-- expires (RFC 3339 UTC, '' for never).
ALTER TABLE git_servers ADD COLUMN oauth_client_id TEXT NOT NULL DEFAULT '';
ALTER TABLE git_servers ADD COLUMN oauth_client_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE git_servers ADD COLUMN oauth_refresh_token TEXT NOT NULL DEFAULT '';
ALTER TABLE git_servers ADD COLUMN oauth_redirect_uri TEXT NOT NULL DEFAULT '';
ALTER TABLE git_servers ADD COLUMN token_expires_at TEXT NOT NULL DEFAULT '';
