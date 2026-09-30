-- Whether the subscription page offers Telegram: binding an account that has none
-- (sub_tg_bind, on) and moving a linked one to another Telegram (sub_tg_rebind, off:
-- whoever holds the page's link could take the bot account, so the operator opts
-- in). With rebinding off the bot refuses a move however its link was obtained.
ALTER TABLE settings ADD COLUMN sub_tg_bind INTEGER NOT NULL DEFAULT 1;
ALTER TABLE settings ADD COLUMN sub_tg_rebind INTEGER NOT NULL DEFAULT 0;
