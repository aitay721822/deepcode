-- The language a snapshot's wiki is written in (DEEPCODE_WIKI_LANGUAGE when
-- its pages were stored): a new snapshot carries pages over only from a wiki
-- in its own language, and a resumed wiki in another language is planned
-- again. '' for wikis planned before this column, which are never carried
-- over, and planned again if unfinished.
ALTER TABLE snapshots ADD COLUMN wiki_language TEXT NOT NULL DEFAULT '';
