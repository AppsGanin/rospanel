-- Reachability from other countries through Globalping: which countries (ISO codes,
-- comma-separated), how many probes in each, how often the monitor runs (minutes,
-- 0 = only on demand), below what share of answering probes a server counts as
-- unreachable, and an optional Globalping token for the higher hourly limit.
ALTER TABLE settings ADD COLUMN reach_countries TEXT    NOT NULL DEFAULT 'RU';
ALTER TABLE settings ADD COLUMN reach_probes    INTEGER NOT NULL DEFAULT 10;
ALTER TABLE settings ADD COLUMN reach_every     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN reach_threshold INTEGER NOT NULL DEFAULT 50;
ALTER TABLE settings ADD COLUMN reach_token     TEXT    NOT NULL DEFAULT '';

-- The notice about a server unreachable from a country (bit 8192) is on for whoever
-- picked their notices by hand; -1 already holds every bit.
UPDATE settings SET tg_admin_events = tg_admin_events | 8192 WHERE tg_admin_events <> -1;
