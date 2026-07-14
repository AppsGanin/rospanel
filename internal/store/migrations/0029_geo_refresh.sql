-- Auto-refresh cadence for the geo databases (geosite.dat / geoip.dat), in hours.
-- 0 ⇒ never (the pre-existing behaviour: download once if missing, refresh only on
-- the manual button). A positive value makes the panel re-download stale geo on a
-- timer, and it's pushed to nodes so their agents auto-refresh on the same cadence.
ALTER TABLE settings ADD COLUMN geo_refresh_hours INTEGER NOT NULL DEFAULT 0;
