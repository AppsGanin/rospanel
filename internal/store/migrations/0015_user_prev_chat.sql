-- Remember the Telegram chat a user was unlinked from, so pressing
-- "Зарегистрироваться" again from the same chat restores that account
-- (no new user row, no fresh trial) instead of minting a new trial account.
ALTER TABLE users ADD COLUMN tg_prev_chat_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_users_tg_prev_chat ON users(tg_prev_chat_id) WHERE tg_prev_chat_id <> 0;
