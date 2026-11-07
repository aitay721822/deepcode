-- A repository's id in the API, its URLs and the UI is a CUID2, from cuid()
-- (registered by the storage package), not its row id, which counts up and
-- so lets anyone guess the others. Joins and foreign keys keep the row id.
-- Every repository gets one here; the trigger gives one to every repository
-- inserted from now on, so no INSERT has to name it.
ALTER TABLE repositories ADD COLUMN public_id TEXT;
UPDATE repositories SET public_id = cuid();
CREATE UNIQUE INDEX repositories_public_id ON repositories(public_id);
CREATE TRIGGER repositories_public_id_fill AFTER INSERT ON repositories WHEN new.public_id IS NULL BEGIN
  UPDATE repositories SET public_id = cuid() WHERE id = new.id;
END;
