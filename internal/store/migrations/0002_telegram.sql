-- Telegram bot integration (Settings → Telegram). An authorized admin chat can
-- view/add/remove users and receives scheduled backups. Added as a separate
-- migration (ALTER TABLE) so existing installs gain the columns; the consolidated
-- 0001 schema only runs on a fresh database.
ALTER TABLE settings ADD COLUMN tg_bot_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN tg_bot_token   TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN tg_chat_ids    TEXT    NOT NULL DEFAULT ''; -- comma-separated authorized chat IDs
ALTER TABLE settings ADD COLUMN tg_link_code   TEXT    NOT NULL DEFAULT ''; -- pending one-time linking code
ALTER TABLE settings ADD COLUMN tg_backup_cron TEXT    NOT NULL DEFAULT ''; -- 5-field cron in the operator TZ; empty = off
