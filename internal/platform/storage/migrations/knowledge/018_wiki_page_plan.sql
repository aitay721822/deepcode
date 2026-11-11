-- A page's plan as code holds it: the files the model chose for it, those
-- placed beside them, and the units it covers
-- ({"chosen":[...],"placed":[...],"units":[...]}). NULL for pages planned
-- before it, whose wiki the next snapshot plans anew.
ALTER TABLE wiki_pages ADD COLUMN files TEXT;

-- The wiki's fixed headings in its language ({"stack","otherFiles"}), as its
-- outline gave them. NULL for wikis planned before it.
ALTER TABLE snapshots ADD COLUMN wiki_labels TEXT;
