-- Every id the API, the URLs and the UI show is a CUID2, from cuid(), as a
-- repository's is (010): users, roles, API tokens, git servers, snapshots,
-- conversations, their messages and jobs. A row id counts up, so it tells how
-- many came before and lets anyone guess the others. Joins, foreign keys and
-- the code inside keep the row id. Each row gets one here, and a trigger gives
-- one to each row inserted from now on, so no INSERT has to name it.

ALTER TABLE users ADD COLUMN public_id TEXT;
UPDATE users SET public_id = cuid();
CREATE UNIQUE INDEX users_public_id ON users(public_id);
CREATE TRIGGER users_public_id_fill AFTER INSERT ON users WHEN new.public_id IS NULL BEGIN
  UPDATE users SET public_id = cuid() WHERE id = new.id;
END;

ALTER TABLE roles ADD COLUMN public_id TEXT;
UPDATE roles SET public_id = cuid();
CREATE UNIQUE INDEX roles_public_id ON roles(public_id);
CREATE TRIGGER roles_public_id_fill AFTER INSERT ON roles WHEN new.public_id IS NULL BEGIN
  UPDATE roles SET public_id = cuid() WHERE id = new.id;
END;

ALTER TABLE api_tokens ADD COLUMN public_id TEXT;
UPDATE api_tokens SET public_id = cuid();
CREATE UNIQUE INDEX api_tokens_public_id ON api_tokens(public_id);
CREATE TRIGGER api_tokens_public_id_fill AFTER INSERT ON api_tokens WHEN new.public_id IS NULL BEGIN
  UPDATE api_tokens SET public_id = cuid() WHERE id = new.id;
END;

ALTER TABLE git_servers ADD COLUMN public_id TEXT;
UPDATE git_servers SET public_id = cuid();
CREATE UNIQUE INDEX git_servers_public_id ON git_servers(public_id);
CREATE TRIGGER git_servers_public_id_fill AFTER INSERT ON git_servers WHEN new.public_id IS NULL BEGIN
  UPDATE git_servers SET public_id = cuid() WHERE id = new.id;
END;

ALTER TABLE snapshots ADD COLUMN public_id TEXT;
UPDATE snapshots SET public_id = cuid();
CREATE UNIQUE INDEX snapshots_public_id ON snapshots(public_id);
CREATE TRIGGER snapshots_public_id_fill AFTER INSERT ON snapshots WHEN new.public_id IS NULL BEGIN
  UPDATE snapshots SET public_id = cuid() WHERE id = new.id;
END;

ALTER TABLE conversations ADD COLUMN public_id TEXT;
UPDATE conversations SET public_id = cuid();
CREATE UNIQUE INDEX conversations_public_id ON conversations(public_id);
CREATE TRIGGER conversations_public_id_fill AFTER INSERT ON conversations WHEN new.public_id IS NULL BEGIN
  UPDATE conversations SET public_id = cuid() WHERE id = new.id;
END;

ALTER TABLE messages ADD COLUMN public_id TEXT;
UPDATE messages SET public_id = cuid();
CREATE UNIQUE INDEX messages_public_id ON messages(public_id);
CREATE TRIGGER messages_public_id_fill AFTER INSERT ON messages WHEN new.public_id IS NULL BEGIN
  UPDATE messages SET public_id = cuid() WHERE id = new.id;
END;

ALTER TABLE jobs ADD COLUMN public_id TEXT;
UPDATE jobs SET public_id = cuid();
CREATE UNIQUE INDEX jobs_public_id ON jobs(public_id);
CREATE TRIGGER jobs_public_id_fill AFTER INSERT ON jobs WHEN new.public_id IS NULL BEGIN
  UPDATE jobs SET public_id = cuid() WHERE id = new.id;
END;
