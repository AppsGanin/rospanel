-- Outbound webhooks: the panel POSTs lifecycle events (user created/expired,
-- payment paid, …) to these endpoints so an external system can react without
-- polling. secret is the HMAC-SHA256 signing key (encrypted at rest); events is a
-- comma-separated set of subscribed event keys (empty ⇒ all events). last_* record
-- the most recent delivery outcome for the settings UI.
CREATE TABLE IF NOT EXISTS webhooks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    url             TEXT    NOT NULL,
    secret          TEXT    NOT NULL,
    events          TEXT    NOT NULL DEFAULT '',
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      INTEGER NOT NULL,
    last_status     INTEGER NOT NULL DEFAULT 0,
    last_attempt_at INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT    NOT NULL DEFAULT ''
);
