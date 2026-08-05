-- System proxies: a SOCKS5 and/or HTTP forward proxy on any server, master or node.
--
-- This generalises "proxy mode", which was master-only, one protocol at a time, and
-- reachable through no screen in the panel — it existed so ANOTHER RosPanel could
-- chain its egress through this one. The same listener is useful for far more than
-- that: pointing a scraper, a bot or somebody else's service at a server and having
-- it come out where the VPN comes out. So it becomes a per-server feature with both
-- protocols available at once.
--
-- These proxies have nothing to do with VPN users: no user's credential opens them,
-- they are not gated by access groups, and they never appear in a subscription. They
-- carry their own accounts, which the operator hands to whatever they are proxying.
-- Traffic from them follows that server's routing, so WARP / Opera / the proxy pools
-- apply exactly as they do for VPN clients.
ALTER TABLE settings ADD COLUMN proxy_socks_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN proxy_socks_port    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN proxy_http_enabled  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN proxy_http_port     INTEGER NOT NULL DEFAULT 0;

-- The logins the proxy accepts, as a JSON array: [{"user":…,"pass":…}]. Several,
-- because one credential for every consumer means revoking the bot's access also
-- breaks the scraper and the colleague who was handed the same string.
--
-- They are per server (a leaked login opens one machine, not the fleet), and each
-- PASSWORD carries the at-rest envelope individually rather than the array being
-- encrypted as a whole — which keeps the logins readable in the database for anyone
-- debugging "which account is this?", and lets the copy below move the previous
-- (already-encrypted) password over verbatim.
--
-- A child table would buy nothing: the list is read and written only as a unit,
-- belongs to exactly one server, and is never queried across servers.
ALTER TABLE settings ADD COLUMN proxy_accounts TEXT NOT NULL DEFAULT '';

-- Carry the master's existing proxy-mode configuration into the new shape, so an
-- install that was already chaining through this server keeps working across the
-- upgrade rather than going dark on the next reconcile.
-- json_quote is what keeps a password containing a quote or a backslash from
-- producing invalid JSON — string concatenation here would have been a corruption bug
-- that only shows up on the operators unlucky enough to have such a password.
UPDATE settings SET
    proxy_socks_enabled = CASE WHEN proxy_mode_enabled = 1 AND proxy_mode_type <> 'http' THEN 1 ELSE 0 END,
    proxy_socks_port    = CASE WHEN proxy_mode_type <> 'http' THEN proxy_mode_port ELSE 0 END,
    proxy_http_enabled  = CASE WHEN proxy_mode_enabled = 1 AND proxy_mode_type =  'http' THEN 1 ELSE 0 END,
    proxy_http_port     = CASE WHEN proxy_mode_type =  'http' THEN proxy_mode_port ELSE 0 END,
    proxy_accounts      = CASE
        WHEN proxy_mode_user <> '' OR proxy_mode_pass <> ''
        THEN '[{"user":' || json_quote(proxy_mode_user) || ',"pass":' || json_quote(proxy_mode_pass) || '}]'
        ELSE '' END;

-- The old shape is gone rather than left behind: two sources of truth for the same
-- listener is how one of them silently stops being read. SQLite ≥3.35 supports DROP
-- COLUMN; none of these carry an index or constraint.
ALTER TABLE settings DROP COLUMN proxy_mode_enabled;
ALTER TABLE settings DROP COLUMN proxy_mode_type;
ALTER TABLE settings DROP COLUMN proxy_mode_port;
ALTER TABLE settings DROP COLUMN proxy_mode_user;
ALTER TABLE settings DROP COLUMN proxy_mode_pass;

-- Nodes get their own, in the same shape. Like WARP and Opera, these are the node's
-- OWN and are never inherited from the master: inheriting would open a listener on
-- every node the moment the master enabled one, and would put the master's password
-- on every node's disk.
ALTER TABLE nodes ADD COLUMN proxy_socks_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN proxy_socks_port    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN proxy_http_enabled  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN proxy_http_port     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN proxy_accounts      TEXT NOT NULL DEFAULT '';
