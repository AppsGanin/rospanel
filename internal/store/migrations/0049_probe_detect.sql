-- Secret-path probe detection: notice IPs that scan for the hidden panel by
-- requesting many distinct paths the decoy site does not have. Detection only
-- records the scanner (it never changes the reply — a flagged IP still gets the
-- same decoy, so the masquerade holds); the operator can then firewall the IPs.

ALTER TABLE settings ADD COLUMN probe_detect INTEGER NOT NULL DEFAULT 1;

-- One row per scanning IP (upserted), so a persistent scanner is a single row
-- rather than one per request. `paths` is the largest distinct-miss burst seen,
-- `hits` how many times this IP crossed the threshold.
CREATE TABLE probe_hits (
    ip         TEXT    NOT NULL PRIMARY KEY,
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    hits       INTEGER NOT NULL DEFAULT 0,
    paths      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_probe_hits_last ON probe_hits(last_seen);
