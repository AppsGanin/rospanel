-- Notifications the panel sends to the USER, through the user bot.
--
-- Until now the panel only ever told the operator that somebody's subscription ran
-- out. The person it actually happened to learned by trying to connect and failing —
-- which is both the worst moment to find out and the point at which they write to
-- support instead of renewing.
ALTER TABLE settings ADD COLUMN tg_user_events INTEGER NOT NULL DEFAULT -1;

-- How many days before expiry the warning goes out. A single knob rather than a set
-- of thresholds: two reminders for one expiry read as nagging, and the operator can
-- pick the horizon that matches their renewal flow.
ALTER TABLE settings ADD COLUMN tg_user_expiring_days INTEGER NOT NULL DEFAULT 3;

-- The expiry a user was already warned about, so the reminder fires once rather than
-- on every poll. Storing the expiry itself (not a timestamp) re-arms it for free:
-- renewing moves expire_at, the stored value stops matching, and the next cycle is
-- eligible again.
ALTER TABLE users ADD COLUMN notified_expire_at INTEGER NOT NULL DEFAULT 0;

-- The quota warning needs its own marker: usage grows inside one limit, so unlike an
-- expiry there is no changing value to key off. 0 = not yet warned; it is cleared
-- again whenever usage drops back under the threshold, which is exactly what a quota
-- reset or a plan change does.
ALTER TABLE users ADD COLUMN notified_quota_at INTEGER NOT NULL DEFAULT 0;
