-- Multi-node: a panel manages N remote VPN servers ("nodes"). Each node runs the
-- same rospanel binary in node mode, holds an outbound long-poll to this panel, and
-- serves the same working user set with its own host/certs/REALITY identity.
--
-- The panel's OWN local Xray is NOT a row here: it stays the settings singleton and
-- is exposed as virtual node 0, so settings remain the single source of truth for it.
-- Anything that iterates servers iterates [node 0] + these rows.
--
-- (This is the squashed schema for the whole multi-node feature. The per-node
-- protocol/ACME/geo columns below were introduced incrementally during development;
-- they're consolidated here since the feature ships as one unit.)
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
    -- reality_dest is this node's own masquerade donor SNI; '' ⇒ inherit the panel's.
    reality_private_key  TEXT NOT NULL DEFAULT '',
    reality_public_key   TEXT NOT NULL DEFAULT '',
    reality_short_id     TEXT NOT NULL DEFAULT '',
    reality_service_name TEXT NOT NULL DEFAULT '',
    reality_dest         TEXT NOT NULL DEFAULT '',

    -- Per-node protocol overrides. NULL ⇒ off (a node's protocols are its OWN — a new
    -- node starts with everything off, no inheritance from the master).
    vless_enabled    INTEGER,
    trojan_enabled   INTEGER,
    hysteria_enabled INTEGER,
    reality_enabled  INTEGER,

    -- Masquerade site served by this node's fallback. Seeded at random on create so
    -- nodes don't all share the panel's fingerprint.
    decoy_template TEXT NOT NULL DEFAULT '',

    -- Per-node overrides that fall back to the global settings when unset:
    --   routing_config: '' ⇒ inherit the panel's routing; a JSON RoutingConfig ⇒
    --     this node's own rules. Egress lanes live in Routing.Lanes and resolve
    --     against the node's own proxy pool; every server's egress is its own.
    --   xray_dns: NULL ⇒ inherit the panel's DNS; any value (incl. '') ⇒ this node's.
    --   connections_config: '' ⇒ inherit the master's transport; a JSON blob ⇒ the
    --     node's own transport (ports, hop, WS path, REALITY port, uTLS fp, anti-DPI).
    routing_config     TEXT NOT NULL DEFAULT '',
    xray_dns           TEXT,
    connections_config TEXT NOT NULL DEFAULT '',

    -- Per-node egress backends, independent of the master, all off by default. WARP is
    -- this node's OWN Cloudflare registration (a shared WireGuard identity across
    -- servers is unsafe); the private key is encrypted at rest. Opera runs the
    -- opera-proxy helper on the node's agent; only the country is stored.
    warp_enabled     INTEGER NOT NULL DEFAULT 0,
    warp_private_key TEXT    NOT NULL DEFAULT '',
    warp_public_key  TEXT    NOT NULL DEFAULT '',
    warp_endpoint    TEXT    NOT NULL DEFAULT '',
    warp_address_v4  TEXT    NOT NULL DEFAULT '',
    warp_address_v6  TEXT    NOT NULL DEFAULT '',
    warp_reserved    TEXT    NOT NULL DEFAULT '',
    opera_enabled    INTEGER NOT NULL DEFAULT 0,
    opera_country    TEXT    NOT NULL DEFAULT '',

    -- Per-node ACME: its OWN CA provider (Let's Encrypt / ZeroSSL) + e-mail, so its
    -- Домен tab matches the master's. '' ⇒ inherit the panel's. ZeroSSL EAB creds are
    -- fetched by the panel and pushed to the node; the HMAC is encrypted at rest.
    acme_email       TEXT NOT NULL DEFAULT '',
    acme_provider    TEXT NOT NULL DEFAULT '',
    zerossl_eab_kid  TEXT NOT NULL DEFAULT '',
    zerossl_eab_hmac TEXT NOT NULL DEFAULT '',

    -- Per-node geo auto-refresh cadence (hours; 0 ⇒ never). Defaults to weekly.
    geo_refresh_hours INTEGER NOT NULL DEFAULT 168,

    -- Reported by the node on every sync.
    last_seen        INTEGER NOT NULL DEFAULT 0,
    node_version     TEXT    NOT NULL DEFAULT '',
    xray_version     TEXT    NOT NULL DEFAULT '',
    xray_running     INTEGER NOT NULL DEFAULT 0,
    -- The node's live cert: links to a node whose cert isn't CA-trusted must carry a
    -- pin (pinnedPeerCertSha256), and the panel can't read a remote node's disk.
    -- issuer ≈ the ACME provider that signed it; expires_at drives the Домен tab.
    cert_sha256      TEXT    NOT NULL DEFAULT '',
    cert_self_signed INTEGER NOT NULL DEFAULT 1,
    cert_issuer      TEXT    NOT NULL DEFAULT '',
    cert_expires_at  INTEGER NOT NULL DEFAULT 0,
    config_hash      TEXT    NOT NULL DEFAULT '', -- desired-state hash the node confirmed applied
    last_report_id   INTEGER NOT NULL DEFAULT 0,  -- traffic-ingest idempotency watermark

    -- Soft-delete tombstone. A deleted node keeps its row (and token) but is hidden
    -- from the operator; its next sync is answered Revoked=true so it stops serving,
    -- instead of silently running the last config forever. A retention sweep purges
    -- tombstones after a grace window.
    deleted_at INTEGER NOT NULL DEFAULT 0,

    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- Unique node names (case-insensitive) among live nodes, a DB backstop to the
-- app-level check: two concurrent creates/renames could otherwise both pass it and
-- produce duplicate names, which collide as Clash proxy names / sing-box tags and make
-- a client drop a whole server. Tombstoned nodes are excluded, so a name is reusable
-- after a node is deleted.
CREATE UNIQUE INDEX idx_nodes_name_live ON nodes(lower(name)) WHERE deleted_at = 0;

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

-- The URL segment the node sync API is mounted under (kept separate from api_path and
-- the panel secret so rotating either never orphans a node). Generated when the first
-- node is created; empty ⇒ the surface doesn't exist and nodes fall to decoy.
ALTER TABLE settings ADD COLUMN node_api_path TEXT NOT NULL DEFAULT '';

-- Display name of the panel's own server (the "master") shown in share-link /
-- subscription config labels, so a multi-node user can tell the master's entries apart
-- from the nodes'. Empty ⇒ no prefix (single-server default).
ALTER TABLE settings ADD COLUMN master_label TEXT NOT NULL DEFAULT '';

-- Auto-refresh cadence for the master's geo databases (geosite.dat / geoip.dat), in
-- hours; 0 ⇒ never. Defaults to weekly. Pushed to nodes so agents refresh on cadence.
ALTER TABLE settings ADD COLUMN geo_refresh_hours INTEGER NOT NULL DEFAULT 168;
