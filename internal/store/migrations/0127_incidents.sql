-- Incidents and planned maintenance, written by the operator and shown on the public
-- status page ("works on the NL server until 15:00").
--
-- kind is 'incident' (something broke; open until resolved) or 'maintenance' (a
-- planned window: upcoming before starts_at, over at ends_at or when resolved by
-- hand). node_ids is a JSON array of the servers it concerns, node 0 being the
-- panel's own; an empty array means the whole service. No foreign key, the same as
-- uptime_daily: a deleted server's past incidents still happened.
CREATE TABLE incidents (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT    NOT NULL DEFAULT 'incident',
    title       TEXT    NOT NULL,
    body        TEXT    NOT NULL DEFAULT '',
    node_ids    TEXT    NOT NULL DEFAULT '[]',
    starts_at   INTEGER NOT NULL,
    ends_at     INTEGER NOT NULL DEFAULT 0,
    resolved_at INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE INDEX idx_incidents_starts_at ON incidents(starts_at);
