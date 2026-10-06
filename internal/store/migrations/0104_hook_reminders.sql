-- Reminders for an external system, sent as webhooks: user.expiring when a term ends
-- within the Telegram bot's warning horizon and user.traffic_low at the bot's quota
-- share, each to whoever subscribed to it. Markers of their own, apart from the
-- bot's: a site's users often have no Telegram. hook_expire_at is the expiry a
-- reminder went out for (a renewal re-arms it), hook_quota_at marks the traffic
-- reminder (cleared once usage drops back under the line).
ALTER TABLE users ADD COLUMN hook_expire_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN hook_quota_at INTEGER NOT NULL DEFAULT 0;
