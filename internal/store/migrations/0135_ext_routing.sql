-- How a relayed external subscription's traffic leaves our server (model.ExtRouting):
-- rules sending some of it elsewhere than the partner (direct, WARP, a lane…), where
-- the rest goes, and what our server dials the partner through. JSON; empty is the
-- old behaviour — all of it to the partner, dialled directly.
ALTER TABLE ext_subscriptions ADD COLUMN routing TEXT NOT NULL DEFAULT '';

-- A subscription can be relayed through several of our servers, each over the lanes
-- the operator picks: one row per (server, lane). The single relay_lane /
-- relay_server_id pair before this is carried over and no longer read.
CREATE TABLE ext_relays (
    sub_id    INTEGER NOT NULL REFERENCES ext_subscriptions(id) ON DELETE CASCADE,
    server_id INTEGER NOT NULL,
    lane      TEXT    NOT NULL,
    PRIMARY KEY (sub_id, server_id, lane)
) WITHOUT ROWID;
INSERT INTO ext_relays (sub_id, server_id, lane)
    SELECT id, relay_server_id, relay_lane FROM ext_subscriptions WHERE relay_lane <> '';
