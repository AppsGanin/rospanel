-- External REST API access. Each key is a named credential a surrounding system
-- authenticates with (Authorization: Bearer <key>). Only the HMAC-SHA256 hash of
-- the raw key is stored (peppered with settings.session_pepper, same as admin
-- sessions) — the raw key is shown once at creation and never recoverable.
--
-- prefix is the leading, non-secret part of the key (e.g. "rp_A1b2C3") kept in
-- clear so the operator can tell keys apart in the UI. Every key grants full
-- access to the API.
CREATE TABLE IF NOT EXISTS api_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL,
    key_hash     TEXT    NOT NULL UNIQUE,
    prefix       TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL DEFAULT 0,
    revoked_at   INTEGER NOT NULL DEFAULT 0
);

-- Random, unguessable, stable URL segment the external API is mounted under
-- (/<api_path>/v1/...). Kept separate from panel_secret_path so rotating the
-- panel secret never breaks live integrations. Empty ⇒ the API surface is off.
ALTER TABLE settings ADD COLUMN api_path TEXT NOT NULL DEFAULT '';
