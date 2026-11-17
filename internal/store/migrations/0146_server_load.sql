-- Load history per server, for the charts on the Servers page: CPU, RAM and
-- whether the server was up, over the last day, week or month.
--
-- One row per server per five minutes, folded from the minute samples the node
-- watch tick already takes for the status page (sums and counts, so a coarser view
-- is a GROUP BY and an average stays exact). A month is 8,640 rows a server.
-- stats counts the samples that carried CPU/RAM: a node that is down reports
-- nothing, and its silence must not read as an idle machine.
-- node_id 0 is the panel's own server; no foreign key, like uptime_daily.
CREATE TABLE server_load (
    node_id INTEGER NOT NULL,
    ts      INTEGER NOT NULL,              -- bucket start, unix seconds, a multiple of 300
    cpu_sum REAL    NOT NULL DEFAULT 0,    -- percent, summed over `stats` samples
    cpu_max REAL    NOT NULL DEFAULT 0,
    mem_sum REAL    NOT NULL DEFAULT 0,    -- percent of RAM in use
    mem_max REAL    NOT NULL DEFAULT 0,
    stats   INTEGER NOT NULL DEFAULT 0,
    up      INTEGER NOT NULL DEFAULT 0,
    total   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (node_id, ts)
) WITHOUT ROWID;

CREATE INDEX idx_server_load_ts ON server_load(ts);

-- When each server was down: one row per outage, ended_at 0 while it lasts. The
-- five-minute rows say how much of a bucket was down; this says when it started,
-- how long it took and how often it happens.
CREATE TABLE server_outages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id    INTEGER NOT NULL,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_server_outages_node ON server_outages(node_id, started_at);
