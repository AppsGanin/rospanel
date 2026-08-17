-- Wedged-process watchdog toggle. The auto-recovery (restart a hung-but-alive Xray)
-- is on by default; an operator can switch it off from the master's settings, e.g.
-- while debugging a wedge by hand.
ALTER TABLE settings ADD COLUMN watchdog_enabled INTEGER NOT NULL DEFAULT 1;
