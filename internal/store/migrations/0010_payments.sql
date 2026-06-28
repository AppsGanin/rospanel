-- Automatic payment providers: YooKassa (cards, RUB) and CryptoBot (Telegram
-- crypto). Credentials are stored encrypted at rest (secret_key / token).
-- payment_webhook_secret is a random URL segment so providers can POST to a fixed
-- but unguessable webhook path that doesn't reveal the hidden panel.
ALTER TABLE settings ADD COLUMN yookassa_enabled       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN yookassa_shop_id       TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN yookassa_secret_key    TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN yookassa_test          INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN cryptobot_enabled      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN cryptobot_token        TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN cryptobot_testnet      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN payment_webhook_secret TEXT    NOT NULL DEFAULT '';

-- Per-order provider linkage so a webhook / poll can map an external payment back
-- to its order. provider: '' (manual) | 'yookassa' | 'cryptobot'.
ALTER TABLE payment_orders ADD COLUMN provider    TEXT NOT NULL DEFAULT '';
ALTER TABLE payment_orders ADD COLUMN provider_id TEXT NOT NULL DEFAULT '';
ALTER TABLE payment_orders ADD COLUMN pay_url     TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_payment_orders_provider ON payment_orders(provider, provider_id);
