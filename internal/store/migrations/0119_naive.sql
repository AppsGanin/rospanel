-- NaiveProxy (internal/naive): CONNECT tunnels through the decoy behind :443, handed to
-- Xray's internal naive-in inbound. It uses each user's existing UUID, so only the lane's
-- switch and display name are new — the master's here, a node's switch on its row (its
-- name rides in connections_config, like AmneziaWG's). On by default, everywhere.
ALTER TABLE settings ADD COLUMN naive_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE settings ADD COLUMN naive_name    TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN naive_enabled INTEGER NOT NULL DEFAULT 1;
-- Whether a node's agent answers NaiveProxy tunnels, as it last said. Kept here rather
-- than in memory so a node's NaiveProxy link survives a panel restart, even while the
-- node is offline.
ALTER TABLE nodes ADD COLUMN serves_naive INTEGER NOT NULL DEFAULT 0;
