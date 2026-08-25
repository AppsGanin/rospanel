-- Active admin sessions metadata: IP, user agent, and last seen timestamp.
--
-- This enables viewing and revoking active sessions across devices in the admin panel.
ALTER TABLE admin_sessions ADD COLUMN ip TEXT NOT NULL DEFAULT '';
ALTER TABLE admin_sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE admin_sessions ADD COLUMN last_seen_at INTEGER NOT NULL DEFAULT 0;
