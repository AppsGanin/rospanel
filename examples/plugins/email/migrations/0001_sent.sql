CREATE TABLE sent (
    event_id TEXT NOT NULL,
    user_id  INTEGER NOT NULL,
    at       INTEGER NOT NULL,
    PRIMARY KEY (event_id, user_id)
);
CREATE INDEX sent_at ON sent(at);
