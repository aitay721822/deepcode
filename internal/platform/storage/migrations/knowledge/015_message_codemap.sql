-- An answer drawn as a code map keeps the map (JSON, package codemap) beside
-- its text; other messages have none.
ALTER TABLE messages ADD COLUMN codemap TEXT;
