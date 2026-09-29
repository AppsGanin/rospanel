-- Flexible plans.
--
-- A plan may sell devices beyond the ones it includes: device_price roubles per
-- extra device per period, up to device_max of them. users.extra_devices is how many
-- the user holds — part of their device_limit, and of what a renewal costs.
-- users.pack_data is traffic bought on top of the plan's quota (bytes, already in
-- data_limit); it goes when the quota next resets or a new term starts.
ALTER TABLE tariff_plans ADD COLUMN device_price INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tariff_plans ADD COLUMN device_max   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN extra_devices INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN pack_data     INTEGER NOT NULL DEFAULT 0;

-- What an order buys beyond a plan and its periods. devices: the extra devices a plan
-- order comes with, or how many a 'devices' order adds. change_from: the plan a
-- 'change' order moves away from. expect_expire: the term the price was computed
-- for — a 'change' or 'devices' order that finds another term when the money arrives
-- is not applied (its money lands on the balance). pack_bytes: a 'traffic' order.
ALTER TABLE payment_orders ADD COLUMN devices       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE payment_orders ADD COLUMN change_from   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE payment_orders ADD COLUMN expect_expire INTEGER NOT NULL DEFAULT 0;
ALTER TABLE payment_orders ADD COLUMN pack_bytes    INTEGER NOT NULL DEFAULT 0;

-- traffic_packs: JSON [{gb, price_rub}] on sale to users with a paid plan that has a
-- quota. plan_change: users may switch plans while one is active.
ALTER TABLE settings ADD COLUMN traffic_packs TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN plan_change   INTEGER NOT NULL DEFAULT 1;
