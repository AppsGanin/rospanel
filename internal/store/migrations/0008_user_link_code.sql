-- One-time Telegram bind codes per VPN user (replaces sub-token deep links).
ALTER TABLE users ADD COLUMN tg_link_code    TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN tg_link_code_at INTEGER NOT NULL DEFAULT 0;
