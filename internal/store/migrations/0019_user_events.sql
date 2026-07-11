-- Audit log: one row per thing that happened to a user (admin action, payment,
-- self-service, or a system transition). Deliberately NOT foreign-keyed to users:
-- a "user deleted" event must outlive the row it describes, which is why the name
-- is denormalized here.
CREATE TABLE user_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    user_name  TEXT    NOT NULL DEFAULT '',
    action     TEXT    NOT NULL,
    actor_kind TEXT    NOT NULL DEFAULT 'system',
    actor_name TEXT    NOT NULL DEFAULT '',
    details    TEXT    NOT NULL DEFAULT '',  -- JSON object, "" when there's nothing to add
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- The per-user modal reads (user_id, id DESC); the retention sweep reads created_at.
CREATE INDEX idx_user_events_user ON user_events(user_id, id DESC);
CREATE INDEX idx_user_events_created ON user_events(created_at);
