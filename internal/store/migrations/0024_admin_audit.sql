-- The admin audit trail: what was done to the panel itself, and by whom.
--
-- Separate from user_events, which is user-scoped (its user_id is NOT NULL and every
-- row hangs off a user). These events have no user: "the owner rotated the secret
-- path", "someone signed in from 1.2.3.4", "an admin was deleted". Same shape
-- otherwise, so the journal UI reads the same way.
CREATE TABLE admin_audit (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    action     TEXT    NOT NULL,           -- model.Audit* key
    target     TEXT    NOT NULL DEFAULT '', -- what it was done TO (an admin login, a key name); "" when the action says it all
    actor_kind TEXT    NOT NULL,
    actor_name TEXT    NOT NULL DEFAULT '',
    ip         TEXT    NOT NULL DEFAULT '', -- where it came from; the point of a sign-in row
    details    TEXT    NOT NULL DEFAULT '', -- JSON object, or ""
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- The journal reads newest-first and pages backwards by id; the retention sweep
-- deletes by created_at.
CREATE INDEX idx_admin_audit_created ON admin_audit(created_at);
