-- mieru (internal/mieru): its own port, TCP or UDP, answered by the panel and handed to
-- Xray's internal mieru-in inbound on each user's UUID — only the lane's switch, port,
-- transport and display name are new. On by default; a port of 0 is picked by the
-- panel (core.defaultMieruPort) the first time the lane runs.
-- A node's switch is on its row, its port/transport/name ride in connections_config
-- (like AmneziaWG's), and serves_mieru is what its agent last said (like serves_naive).
ALTER TABLE settings ADD COLUMN mieru_enabled   INTEGER NOT NULL DEFAULT 1;
ALTER TABLE settings ADD COLUMN mieru_port      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN mieru_transport TEXT    NOT NULL DEFAULT 'TCP';
ALTER TABLE settings ADD COLUMN mieru_name      TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN mieru_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE nodes ADD COLUMN serves_mieru  INTEGER NOT NULL DEFAULT 0;
