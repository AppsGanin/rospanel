-- Every callback a payment provider sent, with what the panel made of it — the
-- answer to "I paid, where is my plan" without reading the service log.
-- outcome: paid | duplicate | cancelled | refunded | pending | ignored | mismatch |
-- no_order | rejected | error. provider_id and order_id are what the callback named
-- ('' / 0 when it could not be read). headers keep only what explains a signature
-- check; cookies and authorization never land here. body is capped at 16 KB.
CREATE TABLE payment_webhooks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    at          INTEGER NOT NULL,
    provider    TEXT    NOT NULL,
    remote_ip   TEXT    NOT NULL DEFAULT '',
    provider_id TEXT    NOT NULL DEFAULT '',
    order_id    INTEGER NOT NULL DEFAULT 0,
    status      TEXT    NOT NULL DEFAULT '',
    outcome     TEXT    NOT NULL,
    error       TEXT    NOT NULL DEFAULT '',
    headers     TEXT    NOT NULL DEFAULT '',
    body        TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_payment_webhooks_at ON payment_webhooks(at);
CREATE INDEX idx_payment_webhooks_order ON payment_webhooks(order_id) WHERE order_id <> 0;
