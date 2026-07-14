-- Per-node geo auto-refresh cadence (hours; 0 ⇒ never). The node's OWN — separate
-- from the master's — so each server refreshes its geo on its own schedule.
ALTER TABLE nodes ADD COLUMN geo_refresh_hours INTEGER NOT NULL DEFAULT 0;
