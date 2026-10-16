-- Maintenance can take its servers out of subscriptions for its window
-- (incidents.hide_in_sub); a broadcast can wait for its time (scheduled_at, its
-- audience read when it goes out); users may reissue their own subscription link
-- from its page, at most once per sub_self_rotate hours (0, the default: not at all;
-- users.self_rotated_at is when each last did), and move their account to another
-- Telegram at most once per sub_tg_rebind_hours (0: as often as they like;
-- users.tg_changed_at is when it was last linked by its code); and the admin bot
-- sends a summary of each month on the 1st — monthly_report_sent is the month it
-- last did ('YYYY-MM'), so a restart does not send it twice. The summary's alert
-- flag (1 << 14) is on wherever the operator had not chosen the flags one by one.
ALTER TABLE incidents  ADD COLUMN hide_in_sub         INTEGER NOT NULL DEFAULT 0;
ALTER TABLE broadcasts ADD COLUMN scheduled_at        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings   ADD COLUMN sub_self_rotate     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users      ADD COLUMN self_rotated_at     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings   ADD COLUMN sub_tg_rebind_hours INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users      ADD COLUMN tg_changed_at       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings   ADD COLUMN monthly_report_sent TEXT    NOT NULL DEFAULT '';
UPDATE settings SET tg_admin_events = tg_admin_events | 16384 WHERE tg_admin_events <> -1;
