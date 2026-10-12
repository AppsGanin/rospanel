-- Traffic by protocol: what each lane of each server carried per day — Xray's own
-- per-inbound counters (custom inbounds under their tag) and the AmneziaWG tunnel. The
-- per-user table cannot give it: a user's counter is one number across every lane.
CREATE TABLE lane_traffic_daily (
    node_id INTEGER NOT NULL,            -- 0 = the panel's own server
    lane    TEXT    NOT NULL,            -- Xray inbound tag, or "awg"
    day     TEXT    NOT NULL,            -- 'YYYY-MM-DD' (operator-local)
    up      INTEGER NOT NULL DEFAULT 0,
    down    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (node_id, lane, day)
);
CREATE INDEX idx_lane_traffic_daily_day ON lane_traffic_daily(day);
