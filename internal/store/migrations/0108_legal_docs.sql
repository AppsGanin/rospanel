-- The operator's user agreement and privacy policy, in Markdown, shown on the
-- subscription page, in the user bot and over the API. Their own table rather than
-- settings columns: the settings row is read on every request, the documents rarely.
-- legal_path is the random address segment they are served under, so the panel's
-- public paths stay unguessable (as the Mini App's).
CREATE TABLE IF NOT EXISTS legal_docs (
    kind       TEXT PRIMARY KEY,           -- terms | privacy
    body       TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0
);
ALTER TABLE settings ADD COLUMN legal_path TEXT NOT NULL DEFAULT '';
