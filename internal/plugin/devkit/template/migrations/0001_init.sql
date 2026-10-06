-- The plugin's own database. Add migrations as new files; never edit one that shipped.
CREATE TABLE greetings (
    id      INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL,
    text    TEXT NOT NULL
);
