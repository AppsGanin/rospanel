-- Plugins written in the panel: the builder's rules or code by hand, with their
-- tests, kept until they are installed or exported. files is the sources as JSON
-- (path → base64), encrypted like plugin settings: a draft's dev.config.json may
-- hold a real token for trial runs. plugin_id and version are read from its
-- plugin.json on save, for the list.
CREATE TABLE IF NOT EXISTS plugin_drafts (
	id         INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	mode       TEXT NOT NULL DEFAULT 'code',
	plugin_id  TEXT NOT NULL DEFAULT '',
	version    TEXT NOT NULL DEFAULT '',
	files      TEXT NOT NULL,
	created_by TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
