-- Payment providers become rows, not columns. The panel ships with a registry of
-- providers (internal/payments), each declaring its own credential fields, so the
-- schema can't have a column per provider — config is the provider's fields as a
-- JSON object, encrypted at rest as a whole because it holds API keys.
--
-- The old settings.yookassa_* / cryptobot_* columns are left as dead columns:
-- nothing reads them any more, and credentials are re-entered through the new
-- per-provider settings form.
CREATE TABLE payment_providers (
    key     TEXT    PRIMARY KEY,   -- registry key: 'yookassa', 'heleket', …
    enabled INTEGER NOT NULL DEFAULT 0,
    config  TEXT    NOT NULL DEFAULT ''  -- enc:v1: blob of {"field": "value", …}
);
