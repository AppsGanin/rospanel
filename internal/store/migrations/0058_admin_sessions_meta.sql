-- Give every admin session an identity and a trace: an id to revoke it by, the
-- address and client it was opened from, and when it was last used.
--
-- The table was keyed by the token hash alone, which is enough to check a cookie
-- and nothing else: an admin could not see their own open sessions, let alone end
-- one they did not recognise, and a stolen cookie was invisible until it did
-- something. The hash cannot be the handle the panel shows — it must not leave the
-- database — so the table is rebuilt around an integer id. Live sessions survive
-- the rebuild with empty address/client columns; they fill in as those sessions
-- are used (last_seen_at) or stay blank (the address a session was OPENED from is
-- only known at login).
CREATE TABLE admin_sessions_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash   TEXT    NOT NULL UNIQUE,      -- HMAC(pepper, token), hex
    admin_id     INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    expires_at   INTEGER NOT NULL,
    created_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    ip           TEXT    NOT NULL DEFAULT '',  -- last address it was used from
    user_agent   TEXT    NOT NULL DEFAULT '',  -- the browser that opened it
    last_seen_at INTEGER NOT NULL DEFAULT 0    -- last request, stamped no more than once a minute
);
INSERT INTO admin_sessions_new (token_hash, admin_id, expires_at, created_at, last_seen_at)
    SELECT token_hash, admin_id, expires_at, created_at, created_at FROM admin_sessions;
DROP TABLE admin_sessions;
ALTER TABLE admin_sessions_new RENAME TO admin_sessions;
CREATE INDEX idx_sessions_admin ON admin_sessions(admin_id);
