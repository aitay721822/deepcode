-- saved: the running attempt stored work a later attempt skips (1), so it does
-- not count against the job's attempts however it ends.
-- retries: the job's failures in a row for a server it could not reach, which
-- set its backoff; work stored starts the count again.
ALTER TABLE jobs ADD COLUMN saved INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN retries INTEGER NOT NULL DEFAULT 0;
