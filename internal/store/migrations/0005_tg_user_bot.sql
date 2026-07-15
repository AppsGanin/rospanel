-- Separate public user Telegram bot (self-registration + subscription menu).
-- Self-registration is closed by default: an operator opts in by choosing a
-- registration mode after enabling the user bot (0028 turns this bool into a mode;
-- a fresh install seeds mode='off' from this 0). The migration runner keys on the
-- file name, so changing this default only affects databases that never ran 0005.
ALTER TABLE settings ADD COLUMN tg_user_bot_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN tg_user_bot_token   TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN tg_user_reg_enabled INTEGER NOT NULL DEFAULT 0;
