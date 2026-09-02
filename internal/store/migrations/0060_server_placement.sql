-- Where each server sits in a subscription: its country, a manual weight, and the
-- number of users it is meant to carry (model.Placement). The subscription orders
-- servers by these together with the client's country and each server's live load
-- (sub.Order), and can hide a full server until it has room again.
--
-- A node's placement lives on its row; the master has no row in nodes, so its
-- placement lives in settings under master_*. sub_order_mode is the panel-wide
-- policy: manual (the old order), nearest, load, nearest_load.
ALTER TABLE nodes ADD COLUMN country        TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN sort_weight    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN capacity       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN hide_when_full INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN sub_order_mode        TEXT    NOT NULL DEFAULT 'manual';
ALTER TABLE settings ADD COLUMN master_country        TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN master_sort_weight    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN master_capacity       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN master_hide_when_full INTEGER NOT NULL DEFAULT 0;
