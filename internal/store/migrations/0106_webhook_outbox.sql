-- Webhook deliveries waiting to go out, kept in the database instead of a 512-slot
-- queue in memory: a bulk action or a mass reset no longer loses the tail, and a
-- restart no longer loses what was pending. A row is leased (next_at moved ahead)
-- while it is being sent, deleted when it is delivered or out of attempts.
CREATE TABLE IF NOT EXISTS webhook_outbox (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    hook_id    INTEGER NOT NULL,
    event      TEXT    NOT NULL,
    body       BLOB    NOT NULL,
    attempt    INTEGER NOT NULL DEFAULT 0, -- attempts made so far
    next_at    INTEGER NOT NULL,           -- due (or leased until)
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS webhook_outbox_next ON webhook_outbox(next_at);
