-- Per-VPN-user Telegram chat (self-service user bot: subscription + usage stats).
ALTER TABLE users ADD COLUMN tg_chat_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_users_tg_chat ON users(tg_chat_id) WHERE tg_chat_id <> 0;
