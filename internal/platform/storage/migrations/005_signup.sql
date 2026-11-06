-- Whether visitors may create their own account (with the member role): one
-- row, closed until an administrator opens it.
CREATE TABLE signup (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  open INTEGER NOT NULL DEFAULT 0 CHECK (open IN (0, 1))
);
INSERT INTO signup(id) VALUES (1);
