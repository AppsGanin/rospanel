-- The largest single top-up is the operator's to set: a bank connecting a payment
-- system may cap one payment. 15 000 ₽ by default.
ALTER TABLE settings ADD COLUMN wallet_topup_max INTEGER NOT NULL DEFAULT 15000;
