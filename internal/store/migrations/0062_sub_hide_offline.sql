-- Drop a node from subscriptions while it is offline.
--
-- The default is off, and deliberately so: a node bounces on every deploy and cert
-- renewal, and a client whose refresh lands in that window would lose the entry for
-- a server that is already back — while a client that still HAS the entry simply
-- fails over to another one on its own. An operator who would rather hide a dead
-- server than have clients try it can switch this on.
ALTER TABLE settings ADD COLUMN sub_hide_offline INTEGER NOT NULL DEFAULT 0;
