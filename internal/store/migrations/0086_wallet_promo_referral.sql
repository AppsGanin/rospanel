-- Wallet, promo codes and the referral programme.
--
-- Money on the balance is kept in kopecks: a referral percentage of a 199 ₽ plan is
-- 19.90 ₽, and rounding it away would pay the referrer a different sum than the one
-- the settings promise. Prices stay whole roubles.

-- balance_kop is what the user may spend on plans. auto_renew lets the balance pay
-- for the next period when the current one runs out (the user can turn it off).
-- referrer_id is who invited them (0 = nobody), ref_code their own invite code (''
-- until they first open the invite screen). ref_bonus_days are referral days earned
-- while the user had no paid plan to add them to — the next paid period carries them.
-- promo_id is a discount code the user entered and has not paid with yet.
ALTER TABLE users ADD COLUMN balance_kop    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN auto_renew     INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN referrer_id    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN ref_code       TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN ref_bonus_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN promo_id       INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX idx_users_ref_code ON users(ref_code) WHERE ref_code <> '';
CREATE INDEX idx_users_referrer ON users(referrer_id) WHERE referrer_id <> 0;

-- Every change of a balance, with the balance it left behind. The balance column is
-- what is spent; this is how anyone can tell where it came from.
-- kind: topup | purchase | renew | referral | promo | admin
CREATE TABLE balance_tx (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    amount_kop  INTEGER NOT NULL,
    balance_kop INTEGER NOT NULL,
    kind        TEXT    NOT NULL,
    order_id    INTEGER NOT NULL DEFAULT 0,
    ref_user_id INTEGER NOT NULL DEFAULT 0,
    promo_id    INTEGER NOT NULL DEFAULT 0,
    note        TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX idx_balance_tx_user ON balance_tx(user_id, id DESC);

-- kind: percent | amount (a discount on the next payment), days (time on a plan),
-- balance (roubles on the balance). value is the percent, roubles or days.
-- plan_ids limits a discount to these plans ('' = any paid plan); plan_id is the plan
-- a days code grants (0 = adds the days to the user's active paid plan).
-- first_only: a discount only for someone who has never bought a plan.
-- max_uses 0 = unlimited; expires_at 0 = never. Each user may use a code once.
CREATE TABLE promo_codes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    kind       TEXT    NOT NULL,
    value      INTEGER NOT NULL,
    plan_ids   TEXT    NOT NULL DEFAULT '',
    plan_id    INTEGER NOT NULL DEFAULT 0,
    first_only INTEGER NOT NULL DEFAULT 0,
    max_uses   INTEGER NOT NULL DEFAULT 0,
    uses       INTEGER NOT NULL DEFAULT 0,
    expires_at INTEGER NOT NULL DEFAULT 0,
    enabled    INTEGER NOT NULL DEFAULT 1,
    note       TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE TABLE promo_uses (
    promo_id INTEGER NOT NULL,
    user_id  INTEGER NOT NULL,
    order_id INTEGER NOT NULL DEFAULT 0,
    used_at  INTEGER NOT NULL,
    PRIMARY KEY (promo_id, user_id)
);

-- An order either buys a plan or tops up the balance (plan_id 0). amount_rub stays
-- the money that arrives from outside; balance_kop is the part of the price the
-- balance covers, discount_rub what a promo code took off, promo_id that code.
ALTER TABLE payment_orders ADD COLUMN kind         TEXT    NOT NULL DEFAULT 'plan';
ALTER TABLE payment_orders ADD COLUMN balance_kop  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE payment_orders ADD COLUMN discount_rub INTEGER NOT NULL DEFAULT 0;
ALTER TABLE payment_orders ADD COLUMN promo_id     INTEGER NOT NULL DEFAULT 0;
-- "Has this user ever paid?" is now asked per user: the referral reward, a first-
-- payment discount, the invite counts.
CREATE INDEX idx_payment_orders_user ON payment_orders(user_id, status);

-- The referrer a chat arrived with, kept until the chat registers.
ALTER TABLE tg_subscribers ADD COLUMN ref_user_id INTEGER NOT NULL DEFAULT 0;

-- wallet_enabled: users may top up and see their balance. ref_mode: off | percent |
-- days — what a referrer earns when someone they invited pays. ref_first_only limits
-- the reward to that person's first payment.
ALTER TABLE settings ADD COLUMN wallet_enabled   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN wallet_topup_min INTEGER NOT NULL DEFAULT 100;
ALTER TABLE settings ADD COLUMN ref_mode         TEXT    NOT NULL DEFAULT 'off';
ALTER TABLE settings ADD COLUMN ref_percent      INTEGER NOT NULL DEFAULT 10;
ALTER TABLE settings ADD COLUMN ref_days         INTEGER NOT NULL DEFAULT 7;
ALTER TABLE settings ADD COLUMN ref_first_only   INTEGER NOT NULL DEFAULT 0;
