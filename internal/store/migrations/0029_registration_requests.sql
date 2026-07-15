-- Moderated self-registration: a signup is a pending request, not a disabled user.
-- No user account exists until an admin approves the request; on approval the user
-- is created (with the trial/free plan) and the chat linked. One pending request per
-- chat (unique) stops a chat from queuing several.
CREATE TABLE registration_requests (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id    INTEGER NOT NULL UNIQUE,
    name       TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
