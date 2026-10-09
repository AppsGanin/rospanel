-- One draft per plugin, held by the database: a double click on "edit" or a save
-- that renames a draft's plugin to another draft's id could make two. Should two
-- already be there, the last edited stays. rev counts saves: a save made from an
-- older read is refused instead of writing over an edit that came in between.
DELETE FROM plugin_drafts WHERE plugin_id <> '' AND id NOT IN (
	SELECT id FROM (
		SELECT id, ROW_NUMBER() OVER (PARTITION BY plugin_id ORDER BY updated_at DESC, id DESC) AS n
		FROM plugin_drafts WHERE plugin_id <> ''
	) WHERE n = 1
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_drafts_plugin ON plugin_drafts(plugin_id) WHERE plugin_id <> '';
ALTER TABLE plugin_drafts ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;
