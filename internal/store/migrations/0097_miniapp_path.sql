-- The Mini App's address segment (/<sub path>/<miniapp_path>): random per install,
-- so the entrance is not a fixed path a scanner can ask for. Filled in by the panel at
-- start (core.EnsureMiniAppPath), like the payment webhook secret. tg_menu_url is the
-- address the user bot's menu button was last pointed at — the one it may update.
ALTER TABLE settings ADD COLUMN miniapp_path TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN tg_menu_url  TEXT NOT NULL DEFAULT '';
