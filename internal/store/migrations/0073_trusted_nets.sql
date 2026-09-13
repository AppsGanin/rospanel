-- Addresses and networks the panel never bans on its own (Settings → Protection).
--
-- A JSON array of prefixes, normalized on the way in ("203.0.113.10/32",
-- "198.51.100.0/24"). Empty — the default, and what every existing row gets — trusts
-- nobody, which is exactly how every ban behaved before this column existed.
ALTER TABLE settings ADD COLUMN trusted_nets TEXT NOT NULL DEFAULT '[]';
