-- The daily scanner digest is gated purely by the "Path scanners" admin-notification
-- category (model.AdminEventProbe), like every other admin alert — no separate
-- settings toggle. Drop the short-lived probe_digest column (added in 0051).
ALTER TABLE settings DROP COLUMN probe_digest;
