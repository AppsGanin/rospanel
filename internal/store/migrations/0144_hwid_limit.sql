-- Device limits split in two: by address (device_limit) and by HWID (hwid_limit), for
-- users and for tariffs, each -1 = none, 0 = the panel default, N = N. The panel gets
-- an address default beside the HWID one.
--
-- Seeded so nobody's limit changes: device_limit capped both counters until now, so
-- hwid_limit starts as a copy of it, and an address default of 0 is "none" — what
-- device_limit 0 meant.
ALTER TABLE users ADD COLUMN hwid_limit INTEGER NOT NULL DEFAULT 0;
UPDATE users SET hwid_limit = device_limit;

ALTER TABLE tariff_plans ADD COLUMN hwid_limit INTEGER NOT NULL DEFAULT 0;
UPDATE tariff_plans SET hwid_limit = device_limit;

ALTER TABLE settings ADD COLUMN ip_fallback_limit INTEGER NOT NULL DEFAULT 0;
