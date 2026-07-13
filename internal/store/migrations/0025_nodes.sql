-- Multi-node: a panel manages N remote VPN servers ("nodes"). Each node runs the
-- same rospanel binary in node mode, holds an outbound long-poll to this panel,
-- and serves the same working user set with its own host/certs/REALITY identity.
--
-- The panel's OWN local Xray is NOT a row here: it stays the settings singleton
-- and is exposed as virtual node 0, so settings remain the single source of truth
-- for it. Anything that iterates servers iterates [node 0] + these rows.
CREATE TABLE nodes (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT    NOT NULL,
    host TEXT    NOT NULL,                     -- node's own domain/IP: ACME target + link address
    -- A disabled node is dropped from subscriptions and told to stop serving, but
    -- keeps its row (and its token) so it can be switched back on.
    enabled INTEGER NOT NULL DEFAULT 1,

    -- Auth. The permanent bearer token is stored only as an HMAC hash (same shape
    -- as api_keys); the one-time join token is what the install command carries and
    -- is cleared the moment it is exchanged for the permanent one.
    token_hash      TEXT    NOT NULL DEFAULT '',
    join_token_hash TEXT    NOT NULL DEFAULT '',
    join_expires_at INTEGER NOT NULL DEFAULT 0,

    -- Per-node REALITY identity (a shared keypair across nodes would let one
    -- compromised node impersonate the others). Private key encrypted at rest.
    reality_private_key  TEXT NOT NULL DEFAULT '',
    reality_public_key   TEXT NOT NULL DEFAULT '',
    reality_short_id     TEXT NOT NULL DEFAULT '',
    reality_service_name TEXT NOT NULL DEFAULT '',

    -- Per-node protocol overrides. NULL ⇒ inherit the global settings toggle.
    vless_enabled    INTEGER,
    trojan_enabled   INTEGER,
    hysteria_enabled INTEGER,
    reality_enabled  INTEGER,

    -- Masquerade site served by this node's fallback. Seeded at random on create so
    -- nodes don't all share the panel's fingerprint.
    decoy_template TEXT NOT NULL DEFAULT '',

    -- Per-node overrides that fall back to the global settings when unset:
    --   routing_config: '' ⇒ inherit the panel's routing; a JSON RoutingConfig ⇒
    --     this node's own rules (egress lanes/WARP/Opera are dropped on nodes, which
    --     have no such backends, so those rules degrade to direct).
    --   xray_dns: NULL ⇒ inherit the panel's DNS; any value (incl. '') ⇒ this node's.
    routing_config TEXT NOT NULL DEFAULT '',
    xray_dns       TEXT,

    -- Reported by the node on every sync.
    last_seen        INTEGER NOT NULL DEFAULT 0,
    node_version     TEXT    NOT NULL DEFAULT '',
    xray_version     TEXT    NOT NULL DEFAULT '',
    xray_running     INTEGER NOT NULL DEFAULT 0,
    -- The node's live cert: links to a node whose cert isn't CA-trusted must carry a
    -- pin (pinnedPeerCertSha256), and the panel can't read a remote node's disk.
    cert_sha256      TEXT    NOT NULL DEFAULT '',
    cert_self_signed INTEGER NOT NULL DEFAULT 1,
    config_hash      TEXT    NOT NULL DEFAULT '', -- desired-state hash the node confirmed applied
    last_report_id   INTEGER NOT NULL DEFAULT 0,  -- traffic-ingest idempotency watermark

    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- Traffic gains a node dimension. SQLite can't alter a primary key, so rebuild the
-- table; existing history belongs to the local server and becomes node 0.
CREATE TABLE traffic_daily_new (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id INTEGER NOT NULL DEFAULT 0,      -- 0 = the panel's own server (virtual node)
    day     TEXT    NOT NULL,                -- 'YYYY-MM-DD' (operator-local)
    up      INTEGER NOT NULL DEFAULT 0,
    down    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, node_id, day)
);
INSERT INTO traffic_daily_new (user_id, node_id, day, up, down)
    SELECT user_id, 0, day, up, down FROM traffic_daily;
DROP TABLE traffic_daily;
ALTER TABLE traffic_daily_new RENAME TO traffic_daily;
CREATE INDEX idx_traffic_daily_day ON traffic_daily(day);

-- The URL segment the node sync API is mounted under (kept separate from api_path
-- and the panel secret so rotating either never orphans a node). Generated when the
-- first node is created; empty ⇒ the surface doesn't exist and nodes fall to decoy.
ALTER TABLE settings ADD COLUMN node_api_path TEXT NOT NULL DEFAULT '';
