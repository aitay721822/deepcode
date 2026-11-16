-- Set by a full re-index: the snapshot's wiki is planned anew instead of carried from the previous one; cleared
-- once the new plan is stored.
ALTER TABLE snapshots ADD COLUMN wiki_replan INTEGER NOT NULL DEFAULT 0;
