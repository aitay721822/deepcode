-- Registering an app sweeps the apps nobody approved, asking of each whether a
-- code or token still names it: by client_id alone, which 014's indexes, led
-- by user_id, cannot answer without a scan.
CREATE INDEX api_tokens_client ON api_tokens(client_id);
CREATE INDEX oauth_codes_client ON oauth_codes(client_id);
CREATE INDEX oauth_refresh_tokens_client ON oauth_refresh_tokens(client_id);
