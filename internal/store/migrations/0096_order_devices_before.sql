-- A change order records the extra devices the user held on the plan it leaves, so a
-- refund that moves them back gives those back too.
ALTER TABLE payment_orders ADD COLUMN devices_before INTEGER NOT NULL DEFAULT 0;
