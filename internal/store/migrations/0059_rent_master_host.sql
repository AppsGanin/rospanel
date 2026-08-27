-- Rented node master host address tracking and local master sharing settings.
ALTER TABLE nodes ADD COLUMN rent_master_host TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN share_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN share_quota_percent INTEGER NOT NULL DEFAULT 100;
ALTER TABLE settings ADD COLUMN share_speed_limit INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN share_token TEXT NOT NULL DEFAULT '';
