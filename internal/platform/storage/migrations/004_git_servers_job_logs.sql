-- The servers repositories come from: GitHub, GitLab or a self-hosted
-- instance of either. token is sealed with AES-256-GCM ('' for none).
-- api_url is '' unless it overrides the kind's default.
CREATE TABLE git_servers (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE,
  kind TEXT NOT NULL CHECK (kind IN ('github', 'gitlab')),
  base_url TEXT NOT NULL UNIQUE COLLATE NOCASE,
  api_url TEXT NOT NULL DEFAULT '',
  token TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO git_servers(id, name, kind, base_url) VALUES (1, 'GitHub', 'github', 'https://github.com');

-- Every repository imported so far came from GitHub. SQLite cannot add a
-- NOT NULL column with a foreign key, so gitserver refuses to delete a server
-- that repositories still use.
ALTER TABLE repositories ADD COLUMN git_server_id INTEGER NOT NULL DEFAULT 1;
CREATE INDEX repositories_server ON repositories(git_server_id);

-- Output of a job, streamed to the admin console.
CREATE TABLE job_logs (
  id INTEGER PRIMARY KEY,
  job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  line TEXT NOT NULL,
  at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX job_logs_job ON job_logs(job_id, id);

-- A finished job queued again starts with a clean log.
CREATE TRIGGER jobs_requeued AFTER UPDATE OF status ON jobs
WHEN old.status IN ('succeeded', 'failed') AND new.status = 'queued' BEGIN
  DELETE FROM job_logs WHERE job_id = new.id;
END;
