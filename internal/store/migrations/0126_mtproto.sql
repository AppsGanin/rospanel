-- MTProto (internal/mtproto): a Telegram proxy with fake TLS on a port of its own,
-- answered by the panel and handed to Xray's internal mtproto-in inbound on each user's
-- UUID — only the lane's switch, port, display name and the domain in the secrets are
-- stored. The port and the domain are settled by the panel the first time the lane runs
-- and kept: every link a user added carries both. A node's switch and domain are its
-- own; its port and name ride in its connections, and serves_mtproto is what its agent
-- last said.
ALTER TABLE settings ADD COLUMN mtproto_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE settings ADD COLUMN mtproto_port    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN mtproto_name    TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN mtproto_domain  TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN mtproto_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE nodes ADD COLUMN serves_mtproto  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN mtproto_domain  TEXT    NOT NULL DEFAULT '';
