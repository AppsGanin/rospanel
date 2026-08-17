-- Actions on top of secret-path probe detection (both default off — recording alone
-- stays the safe default):
--   probe_block  — drop a flagged scanner's IP at the firewall (nftables)
--   probe_digest — send the operator one daily summary of new scanners
ALTER TABLE settings ADD COLUMN probe_block INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN probe_digest INTEGER NOT NULL DEFAULT 0;
