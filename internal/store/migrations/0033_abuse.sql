-- Abuse detection: matched destinations plus the operator's config for it.

-- Destinations that matched a blocklist.
--
-- Only MATCHES land here. The traffic they are drawn from is high-cardinality and is
-- never persisted at all: a row per destination would multiply the write load by
-- orders of magnitude on a single-connection SQLite pool, and would turn the panel
-- into an archive of everyone's browsing. Matches are rare, so they cost almost
-- nothing to keep and are worth keeping for weeks.
--
-- Rolled up per day rather than one row per hit, for the same reason traffic_daily
-- is: the write load has to stay flat as traffic grows.
CREATE TABLE abuse_matches (
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- 0 = the panel's own server, matching traffic_daily's convention. An abuse
    -- complaint arrives about one server's IP, so which node emitted the traffic is
    -- the first thing the operator needs.
    node_id   INTEGER NOT NULL DEFAULT 0,
    -- The destination that matched. Holds an IP address: matching is against
    -- IP-reputation lists, because a client resolves DNS off the tunnel and encrypts
    -- the SNI, so the destination reaches the panel as a bare address.
    domain    TEXT    NOT NULL,
    category  TEXT    NOT NULL,
    day       TEXT    NOT NULL,   -- 'YYYY-MM-DD', operator-local like traffic_daily
    count     INTEGER NOT NULL DEFAULT 0,
    last_seen INTEGER NOT NULL,
    PRIMARY KEY (user_id, node_id, domain, day)
);

-- Leading with day serves both the "recent abuse" reads and the retention sweep,
-- for the same reason idx_connections_last_seen leads with last_seen.
CREATE INDEX idx_abuse_matches_day ON abuse_matches(day, user_id);

-- Operator config.
--
-- abuse_enabled is the master switch. abuse_categories is a bitmask of the active
-- list categories (see model.AbuseCategoryCatalog) — default -1, all on, so a bit
-- added later is on by default too. abuse_custom is the operator's own IPs/CIDRs, one
-- per line. abuse_alert_min is how many matches a user must accrue in a day before a
-- Telegram alert fires.
ALTER TABLE settings ADD COLUMN abuse_enabled    INTEGER NOT NULL DEFAULT 1;
ALTER TABLE settings ADD COLUMN abuse_categories INTEGER NOT NULL DEFAULT -1;
ALTER TABLE settings ADD COLUMN abuse_custom     TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN abuse_alert_min  INTEGER NOT NULL DEFAULT 20;
