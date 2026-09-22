-- An external subscription can be relayed through one of our own servers: its servers
-- are handed out as entries of that server's VLESS lane, and the server carries the
-- traffic on to them (model.ExtSubscription.RelayLane). Empty lane = handed out as
-- they are, which is what every existing subscription keeps doing.
ALTER TABLE ext_subscriptions ADD COLUMN relay_lane      TEXT    NOT NULL DEFAULT '';
ALTER TABLE ext_subscriptions ADD COLUMN relay_server_id INTEGER NOT NULL DEFAULT 0;
