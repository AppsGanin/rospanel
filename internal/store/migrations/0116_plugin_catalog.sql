-- The plugin catalog: the index the panel lists plugins from ('' = the official
-- one; another address is a mirror, still signed by the official key) and the new
-- versions the operator was already told about, as JSON {plugin: version}.
CREATE TABLE IF NOT EXISTS plugin_catalog (
    id       INTEGER PRIMARY KEY CHECK (id = 1),
    url      TEXT NOT NULL DEFAULT '',
    notified TEXT NOT NULL DEFAULT '{}'
);
INSERT OR IGNORE INTO plugin_catalog (id) VALUES (1);
