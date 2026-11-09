-- TrustTunnel (internal/naive, tt.go): HTTP/2 CONNECT through the decoy behind :443
-- like NaiveProxy, handed to Xray's internal trusttunnel-in inbound on each user's UUID
-- — only the lane's switch and display name are new, on by default. A node's switch is
-- on its row, its name rides in connections_config, and serves_trusttunnel is what its
-- agent last said.
ALTER TABLE settings ADD COLUMN trusttunnel_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE settings ADD COLUMN trusttunnel_name    TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN trusttunnel_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE nodes ADD COLUMN serves_trusttunnel  INTEGER NOT NULL DEFAULT 0;
