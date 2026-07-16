-- The iplist lists get their own auto-refresh cadence, separate from the geo
-- databases. They are a different kind of data on a different clock: the geo .dat
-- files are republished every few days, whereas the iplist services re-resolve
-- their addresses roughly every 12 hours, so an operator wants to poll them more
-- often without dragging ~28 MB of .dat down at the same rate.
ALTER TABLE settings ADD COLUMN iplist_refresh_hours INTEGER NOT NULL DEFAULT 0;

-- Seed from the geo cadence: until now one setting drove both, so anyone with geo
-- auto-refresh on already had their lists refreshed on that schedule. Defaulting
-- to 0 would silently switch that off for them.
UPDATE settings SET iplist_refresh_hours = geo_refresh_hours WHERE id = 1;
