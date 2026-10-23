-- A server's WARP and Opera handed to users as connections of their own: each is a
-- VLESS entry on the lanes picked, whose traffic all leaves through that way out
-- (model.ExitConfig). JSON per server; empty is none.
ALTER TABLE settings ADD COLUMN exit_configs TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes    ADD COLUMN exit_configs TEXT NOT NULL DEFAULT '';
