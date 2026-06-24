-- Separate public user Telegram bot (self-registration + subscription menu).
ALTER TABLE settings ADD COLUMN tg_user_bot_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN tg_user_bot_token   TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN tg_user_reg_enabled INTEGER NOT NULL DEFAULT 1;
