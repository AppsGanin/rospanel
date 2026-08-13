-- Uptime history behind the public status page.
--
-- One row per server per operator-local day, holding how many liveness samples were
-- taken and how many found the server up. That shape is chosen over an event log
-- (outage start/end) because the page renders a bar per day and an uptime
-- percentage — both of which are a division of these two numbers — while an event
-- log would need to be folded into exactly this on every render.
--
-- node_id 0 is the panel's own server, matching traffic_daily and the rest of the
-- per-server tables. No foreign key on purpose: a node's history outlives its
-- deletion for the rest of the retention window, the same way its traffic rows do —
-- the outage happened, and removing the node doesn't unhappen it.
CREATE TABLE uptime_daily (
    node_id INTEGER NOT NULL,
    day     TEXT    NOT NULL,          -- 'YYYY-MM-DD', operator-local
    up      INTEGER NOT NULL DEFAULT 0,
    total   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (node_id, day)
);

-- The page reads a date range across every server, and the sweep deletes by day.
CREATE INDEX idx_uptime_daily_day ON uptime_daily(day);

-- Off by default: the panel's whole posture is that an unknown path is
-- indistinguishable from ordinary hosting, and a status page is a deliberate,
-- operator-made hole in that.
ALTER TABLE settings ADD COLUMN status_enabled INTEGER NOT NULL DEFAULT 0;

-- URL segment the page answers on. Empty falls back to "status".
ALTER TABLE settings ADD COLUMN status_path TEXT NOT NULL DEFAULT 'status';
