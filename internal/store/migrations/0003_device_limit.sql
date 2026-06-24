-- Per-user simultaneous device cap (unique source IPs seen recently). 0 = unlimited.
ALTER TABLE users ADD COLUMN device_limit INTEGER NOT NULL DEFAULT 0;
