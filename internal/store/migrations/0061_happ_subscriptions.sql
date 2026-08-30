-- Happ Subscription: external VPN subscription sources (vless://, vmess://, trojan://,
-- ss://, hysteria2://) that are fetched, decoded, and stored as Happ nodes.
-- Nodes are used as Xray outbounds (egress) when enabled.

CREATE TABLE happ_subscriptions (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT    NOT NULL DEFAULT '',
    url                TEXT    NOT NULL DEFAULT '',
    enabled            INTEGER NOT NULL DEFAULT 1,
    update_interval_min INTEGER NOT NULL DEFAULT 59,
    last_fetch_at      INTEGER NOT NULL DEFAULT 0,
    last_success_at    INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT    NOT NULL DEFAULT '',
    node_count         INTEGER NOT NULL DEFAULT 0,
    created_at         INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE happ_nodes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES happ_subscriptions(id) ON DELETE CASCADE,
    -- identity_key is a deterministic SHA-256 hash for deduplication across syncs.
    -- Computed from: sub_id + protocol + host + port + userinfo (UUID/password).
    identity_key    TEXT    NOT NULL,
    name            TEXT    NOT NULL DEFAULT '',
    protocol        TEXT    NOT NULL DEFAULT '', -- vless | vmess | trojan | ss | hysteria2
    host            TEXT    NOT NULL DEFAULT '',
    port            INTEGER NOT NULL DEFAULT 0,
    enabled         INTEGER NOT NULL DEFAULT 1,
    -- uri is the raw proxy URI stored for Xray outbound generation.
    uri             TEXT    NOT NULL DEFAULT '',
    last_seen_at    INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL DEFAULT 0,
    UNIQUE(identity_key)
);

CREATE INDEX idx_happ_nodes_sub ON happ_nodes(subscription_id);
CREATE INDEX idx_happ_nodes_enabled ON happ_nodes(enabled);
