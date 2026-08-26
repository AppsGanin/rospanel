-- Node rental & resource sharing: allows node owners to share nodes with resource quotas,
-- bandwidth limits, and tenant isolation, while tenants can attach nodes via encrypted links.

ALTER TABLE nodes ADD COLUMN share_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN share_quota_percent INTEGER NOT NULL DEFAULT 100;
ALTER TABLE nodes ADD COLUMN share_speed_limit INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN share_token TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN is_rented INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN rent_owner_node_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN rent_share_key TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN rent_tenant_id TEXT NOT NULL DEFAULT '';

ALTER TABLE inbounds ADD COLUMN tenant_id TEXT NOT NULL DEFAULT '';

CREATE TABLE node_tenants (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id      INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    tenant_id    TEXT    NOT NULL,
    name         TEXT    NOT NULL DEFAULT '',
    traffic_up   INTEGER NOT NULL DEFAULT 0,
    traffic_down INTEGER NOT NULL DEFAULT 0,
    speed_limit  INTEGER NOT NULL DEFAULT 0,
    last_seen    INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL DEFAULT 0,
    UNIQUE(node_id, tenant_id)
);

CREATE INDEX idx_node_tenants_node ON node_tenants(node_id);
