-- ref_days: the bonus days this order's payment gave the referrer (0 = none) — what a
-- chargeback of the order takes back from them.
ALTER TABLE payment_orders ADD COLUMN ref_days INTEGER NOT NULL DEFAULT 0;
