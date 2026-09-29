-- A shared list of Telegram accounts VPN services have banned (resellers, scanners,
-- sharing, fraud), fetched from blacklist_url. With blacklist_enabled a listed
-- account cannot register in the user bot; an account already registered is marked
-- in its card. The list is kept here so a restart or an unreachable GitHub leaves the
-- last copy in force. blacklist_synced_at / blacklist_error are the last fetch.
CREATE TABLE tg_blacklist (
    tg_id  INTEGER PRIMARY KEY,
    reason TEXT    NOT NULL DEFAULT ''
);
ALTER TABLE settings ADD COLUMN blacklist_enabled   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN blacklist_url       TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN blacklist_synced_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN blacklist_error     TEXT    NOT NULL DEFAULT '';
