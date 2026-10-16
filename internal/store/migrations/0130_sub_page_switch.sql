-- The panel's own subscription page can be switched off: a browser opening a
-- subscription link then gets the decoy, like any unknown path, while apps keep
-- fetching the subscription from the same address. On by default.
ALTER TABLE settings ADD COLUMN sub_page_enabled INTEGER NOT NULL DEFAULT 1;
