-- Sign-up from the operator's own website (POST /v1/signup). A site knows its client
-- by its own id — an e-mail, an account number — not by a Telegram chat, so that id
-- is what an account and a request are found by. One account per id: signing up
-- again returns the same account, so an id earns one trial, as a chat does.
ALTER TABLE users ADD COLUMN external_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_users_external_id ON users(external_id) WHERE external_id <> '';

-- A moderated request is a chat's or a website client's. chat_id was NOT NULL UNIQUE,
-- which no request without a chat can satisfy, so the table is rebuilt: each request
-- carries exactly one of the two, and each stays unique (SQLite lets NULLs repeat).
-- A website request keeps what the bot keeps on the chat until the account exists:
-- the source tag it came with and who invited it.
CREATE TABLE registration_requests_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id     INTEGER UNIQUE,
    external_id TEXT    UNIQUE,
    name        TEXT    NOT NULL,
    source      TEXT    NOT NULL DEFAULT '',
    referrer_id INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    CHECK ((chat_id IS NULL) <> (external_id IS NULL))
);
INSERT INTO registration_requests_new (id, chat_id, name, created_at)
    SELECT id, chat_id, name, created_at FROM registration_requests;
-- The id counter carries over, not just the rows: a decided request's id must never
-- come back, because the admin bot's Approve / Reject buttons name requests by it and
-- an old message's button would act on the newcomer.
INSERT INTO sqlite_sequence (name, seq)
    SELECT 'registration_requests_new', 0
     WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'registration_requests_new');
UPDATE sqlite_sequence
   SET seq = max(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'registration_requests'), 0))
 WHERE name = 'registration_requests_new';
DROP TABLE registration_requests;
ALTER TABLE registration_requests_new RENAME TO registration_requests;
