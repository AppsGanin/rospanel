-- Routing/egress snapshots: a point-in-time copy of the routing config so an operator
-- can roll back a change that broke the tunnels (a bad egress lane, a block rule that
-- caught too much). One is taken automatically before every routing change, plus
-- manual ones on demand; the list is capped so it can't grow without bound.
CREATE TABLE config_snapshots (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at   INTEGER NOT NULL,
    label        TEXT    NOT NULL DEFAULT '',
    auto         INTEGER NOT NULL DEFAULT 0,  -- 1 = taken before a change, 0 = manual
    routing_json TEXT    NOT NULL
);
CREATE INDEX idx_config_snapshots_created ON config_snapshots(created_at DESC);
