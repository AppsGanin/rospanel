-- AmneziaWG: a WireGuard tunnel whose handshake hides behind junk packets and
-- random headers (internal/awg). One tunnel per server, with its own keypair and
-- obfuscation parameters; a user is a peer with their own keypair, the same on
-- every server, and a tunnel address derived from their id.
--
-- The master's tunnel lives in settings; a node's on its row (its port and display
-- name ride in connections_config with the other per-node connection settings).
-- Private keys are encrypted at rest like the REALITY ones.
ALTER TABLE settings ADD COLUMN awg_enabled     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN awg_port        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN awg_private_key TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN awg_public_key  TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN awg_params      TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN awg_name        TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN awg_dns         TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN awg_enabled     INTEGER;
ALTER TABLE nodes ADD COLUMN awg_private_key TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN awg_public_key  TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN awg_params      TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN wg_private_key TEXT NOT NULL DEFAULT '';
