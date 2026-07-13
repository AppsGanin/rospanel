-- Two operator settings, both about telling the truth to people who aren't looking
-- at the panel.
--
-- sub_announce: a short message pushed to every VPN client through the subscription
-- response (the `Announce` header, base64 UTF-8 — see internal/server/subscription.go).
-- Clients that support it (Happ, v2RayTun) render it as a line in the app, so it's the
-- only channel to users who never joined the Telegram bot: "the server moves on the
-- 3rd", "your plan expires tomorrow". Empty = no announcement. Clients cap the
-- displayed text at 200 characters, so the panel refuses anything longer.
ALTER TABLE settings ADD COLUMN sub_announce TEXT NOT NULL DEFAULT '';

-- user_autodelete_days: how long an expired user is kept before the retention sweep
-- deletes them. 0 = never delete (the default, and the only safe default: nobody's
-- users should start disappearing because they upgraded the panel). The grace period
-- is counted from the expiry date, not from today, so raising the value resurrects
-- nothing and lowering it doesn't delete anyone retroactively on the next tick — it
-- deletes exactly those already past the new horizon.
ALTER TABLE settings ADD COLUMN user_autodelete_days INTEGER NOT NULL DEFAULT 0;
