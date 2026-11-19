-- An operator's free-text note on a server card: what this node is for, which
-- routes and lanes it carries. Panel-only, never sent to the node. The master
-- keeps its note in settings, like its name.
ALTER TABLE nodes ADD COLUMN note TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN master_note TEXT NOT NULL DEFAULT '';
