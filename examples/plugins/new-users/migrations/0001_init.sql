-- One row per user the plugin announced: what the daily summary counts, and what
-- keeps a repeated delivery of the same event from being announced twice.
CREATE TABLE announced (
    user_id INTEGER PRIMARY KEY,
    name    TEXT    NOT NULL,
    source  TEXT    NOT NULL,
    at      INTEGER NOT NULL
);
