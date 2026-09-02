-- Automatic measures against a user whose traffic keeps hitting the blocklists.
--
-- Until now a user over the daily threshold only produced an alert; what happened
-- next was the operator's. These columns let the panel act on its own, in steps:
-- warn the user through their bot, cap their speed, switch them off — each at its
-- own matches-per-day threshold (0 = that step is off), the last two for a fixed
-- number of hours, after which the panel lifts what it imposed.
ALTER TABLE settings ADD COLUMN abuse_warn_min     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN abuse_throttle_min INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN abuse_throttle_kbps INTEGER NOT NULL DEFAULT 1024;
ALTER TABLE settings ADD COLUMN abuse_disable_min  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN abuse_hours        INTEGER NOT NULL DEFAULT 24;

-- What the panel currently holds against a user, so it can be lifted exactly:
-- which measure ('' | 'throttle' | 'disable'), until when, and the speed cap the
-- throttle replaced. abuse_warned_day keeps the warning to one per day across
-- restarts.
ALTER TABLE users ADD COLUMN abuse_action     TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN abuse_until      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN abuse_prev_speed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN abuse_warned_day TEXT    NOT NULL DEFAULT '';
