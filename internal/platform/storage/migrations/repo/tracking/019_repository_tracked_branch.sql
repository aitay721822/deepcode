-- The branch a repository follows; empty follows its default branch.
ALTER TABLE repositories ADD COLUMN tracked_branch TEXT NOT NULL DEFAULT '';
