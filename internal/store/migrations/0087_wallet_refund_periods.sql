-- Refunds to the balance and paying for several periods at once.

-- refunded_at: when an operator returned the order's money to the user's balance
-- (0 = never). An order is refunded once.
-- periods: how many of the plan's periods the order buys (1 = the plan as sold).
ALTER TABLE payment_orders ADD COLUMN refunded_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE payment_orders ADD COLUMN periods     INTEGER NOT NULL DEFAULT 1;

-- The discounts for buying several periods at once, as JSON
-- [{"periods":3,"percent":10}, …]; '' = one period only.
ALTER TABLE settings ADD COLUMN billing_periods TEXT NOT NULL DEFAULT '';
