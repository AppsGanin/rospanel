-- Operator-defined alert rules ("CPU above 90% for 10 minutes") and their firings.
-- node_id: -1 every server, 0 the master, otherwise a node. alert_events keeps the
-- rule's name and condition so the history outlives the rule; resolved_at = 0 while
-- a firing lasts, and those open rows are how the panel remembers what it already
-- announced across a restart.
CREATE TABLE alert_rules (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    metric     TEXT    NOT NULL,
    op         TEXT    NOT NULL,
    threshold  REAL    NOT NULL,
    for_secs   INTEGER NOT NULL DEFAULT 0,
    node_id    INTEGER NOT NULL DEFAULT -1,
    created_at INTEGER NOT NULL
);

CREATE TABLE alert_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_id     INTEGER NOT NULL,
    rule_name   TEXT    NOT NULL,
    node_id     INTEGER NOT NULL,
    node_name   TEXT    NOT NULL,
    metric      TEXT    NOT NULL,
    op          TEXT    NOT NULL,
    threshold   REAL    NOT NULL,
    value       REAL    NOT NULL,
    peak        REAL    NOT NULL,
    fired_at    INTEGER NOT NULL,
    resolved_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX alert_events_rule ON alert_events (rule_id, resolved_at);
CREATE INDEX alert_events_resolved ON alert_events (resolved_at);

-- The notice about a fired alert rule (bit 32768) is on for whoever picked their
-- notices by hand; -1 already holds every bit.
UPDATE settings SET tg_admin_events = tg_admin_events | 32768 WHERE tg_admin_events <> -1;
