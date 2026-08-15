-- Per-node quota multiplier: scales how fast traffic through a node drains a user's
-- allowance (expensive node > 1, promo node < 1). Only the quota is affected; the
-- per-node byte statistics stay real. 1.0 is the neutral default for every existing
-- and future node.
ALTER TABLE nodes ADD COLUMN traffic_coefficient REAL NOT NULL DEFAULT 1.0;
