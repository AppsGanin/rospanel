-- Refunds the payment provider made, and win-back codes for users who left.

-- refund_source: who returned an order's money — 'balance' (an operator put it on the
-- user's balance) or 'provider' (the payment system refunded it, or a chargeback took
-- it back). A provider refund leaves revenue; a balance refund does not.
ALTER TABLE payment_orders ADD COLUMN refund_source TEXT NOT NULL DEFAULT '';
UPDATE payment_orders SET refund_source = 'balance' WHERE refunded_at > 0;

-- lapsed_at: when the user's paid term ended without renewal (downgraded to the free
-- plan, or cancelled). winback_at: the lapse a win-back code was already sent for.
ALTER TABLE users ADD COLUMN lapsed_at  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN winback_at INTEGER NOT NULL DEFAULT 0;

-- winback_user: the user a personal win-back code was made for (0 = an operator's
-- code). Such codes stay out of the promo list.
ALTER TABLE promo_codes ADD COLUMN winback_user INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_promo_codes_winback ON promo_codes(winback_user) WHERE winback_user <> 0;

-- Win-back: after_days after a paid term lapses, the user gets a one-use discount
-- code of percent %, valid for valid_days.
ALTER TABLE settings ADD COLUMN winback_enabled    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN winback_after_days INTEGER NOT NULL DEFAULT 7;
ALTER TABLE settings ADD COLUMN winback_percent    INTEGER NOT NULL DEFAULT 20;
ALTER TABLE settings ADD COLUMN winback_valid_days INTEGER NOT NULL DEFAULT 7;
