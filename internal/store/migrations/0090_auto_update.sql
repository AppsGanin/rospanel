-- Auto-update: at auto_update_cron (the panel's timezone; '' = off) the panel looks
-- for a newer release and installs it; with auto_update_nodes the servers follow.
-- The last attempt's time and outcome are kept for the settings page.
ALTER TABLE settings ADD COLUMN auto_update_cron    TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN auto_update_nodes   INTEGER NOT NULL DEFAULT 1;
ALTER TABLE settings ADD COLUMN auto_update_last_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN auto_update_last    TEXT    NOT NULL DEFAULT '';

-- The notice about an update (bit 2048) is on for whoever picked their notices by
-- hand; -1 already holds every bit.
UPDATE settings SET tg_admin_events = tg_admin_events | 2048 WHERE tg_admin_events <> -1;
