-- Accounts and role-based access. A user holds roles; a role grants
-- permissions on every repository (all_repositories) or on the ones listed in
-- role_repositories.
CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  display_name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Built-in roles cannot be renamed or deleted; the admin role cannot be changed.
CREATE TABLE roles (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE,
  description TEXT NOT NULL DEFAULT '',
  builtin INTEGER NOT NULL DEFAULT 0,
  all_repositories INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE role_permissions (
  role_id INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  permission TEXT NOT NULL,
  PRIMARY KEY (role_id, permission)
);

CREATE TABLE role_repositories (
  role_id INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  repository_id INTEGER NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  PRIMARY KEY (role_id, repository_id)
);

CREATE TABLE user_roles (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role_id INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, role_id)
);
CREATE INDEX user_roles_role ON user_roles(role_id);

-- token_hash is the SHA-256 of the cookie value: the database alone signs nobody in.
CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX sessions_user ON sessions(user_id);

INSERT INTO roles(id, name, description, builtin, all_repositories) VALUES
  (1, 'admin', 'Every permission on every repository', 1, 1),
  (2, 'member', 'Read and ask about the repositories granted to this role', 1, 0);
INSERT INTO role_permissions(role_id, permission) VALUES
  (1, 'repo.read'), (1, 'repo.chat'), (1, 'repo.import'), (1, 'repo.sync'),
  (1, 'jobs.view'), (1, 'users.manage'), (1, 'servers.manage'),
  (2, 'repo.read'), (2, 'repo.chat');

-- A conversation belongs to the user who started it. Earlier ones are given
-- to the first administrator when the server creates it.
ALTER TABLE conversations ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE CASCADE;
