-- Installed plugins. The package (the zip the operator approved) is kept whole: it
-- is what runs, what the code view shows and what a rollback restores. config holds
-- the operator's settings, encrypted at rest like a payment provider's. granted_*
-- record what the operator consented to — an update asking for more must be
-- consented to again.
CREATE TABLE IF NOT EXISTS plugins (
    id            TEXT PRIMARY KEY,
    version       TEXT    NOT NULL,
    manifest      TEXT    NOT NULL,
    package       BLOB    NOT NULL,
    sha256        TEXT    NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 0,
    status        TEXT    NOT NULL DEFAULT 'disabled', -- active | paused | error | disabled
    status_error  TEXT    NOT NULL DEFAULT '',
    granted_perms TEXT    NOT NULL DEFAULT '',
    granted_net   TEXT    NOT NULL DEFAULT '',
    config        TEXT    NOT NULL DEFAULT '',
    prev_package  BLOB,
    prev_version  TEXT    NOT NULL DEFAULT '',
    sort          INTEGER NOT NULL DEFAULT 0,
    installed_at  INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
