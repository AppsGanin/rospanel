-- Billing: tariff plans, payment orders, per-user plan tracking.

CREATE TABLE tariff_plans (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    slug         TEXT    NOT NULL UNIQUE,
    name         TEXT    NOT NULL,
    price_rub    INTEGER NOT NULL DEFAULT 0,
    period_days  INTEGER NOT NULL DEFAULT 0,
    data_limit   INTEGER NOT NULL DEFAULT 0,
    device_limit INTEGER NOT NULL DEFAULT 0,
    is_free      INTEGER NOT NULL DEFAULT 0,
    payment_url  TEXT    NOT NULL DEFAULT '',
    sort_order   INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE payment_orders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    plan_id    INTEGER NOT NULL,
    amount_rub INTEGER NOT NULL,
    status     TEXT    NOT NULL DEFAULT 'pending',
    created_at INTEGER NOT NULL,
    paid_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_payment_orders_status ON payment_orders(status, created_at DESC);

ALTER TABLE users ADD COLUMN plan_id    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN trial_used INTEGER NOT NULL DEFAULT 0;

ALTER TABLE settings ADD COLUMN billing_enabled       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN billing_trial_days    INTEGER NOT NULL DEFAULT 3;
ALTER TABLE settings ADD COLUMN billing_free_plan_id  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN billing_trial_plan_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN billing_payment_note  TEXT    NOT NULL DEFAULT '';

INSERT INTO tariff_plans (id, slug, name, price_rub, period_days, data_limit, device_limit, is_free, sort_order) VALUES
    (1, 'free',  'Бесплатный', 0,   0, 1073741824,  1, 1, 0),   -- 1 GiB, 1 устройство
    (2, 'trial', 'Пробный',    0,   3, 5368709120,  1, 0, 1),   -- 5 GiB, 1 устройство, 3 дня
    (3, 'month', 'Стандарт',   199, 30, 0,           3, 0, 2);

UPDATE settings SET billing_free_plan_id = 1, billing_trial_plan_id = 2 WHERE id = 1;
