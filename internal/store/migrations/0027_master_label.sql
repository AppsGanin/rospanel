-- master_label is the display name of the panel's own server (the "master") shown
-- in share-link / subscription config labels, so a multi-node user can tell the
-- master's entries apart from the nodes'. Empty ⇒ no prefix (single-server default).
ALTER TABLE settings ADD COLUMN master_label TEXT NOT NULL DEFAULT '';
