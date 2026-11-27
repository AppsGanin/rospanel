-- Which apps a user fetches the subscription with, recorded on every served fetch
-- whether device binding is on or not (the devices table only fills while it is).
-- One row per install: a client that sends x-hwid is keyed by it, one that does not
-- by its app and platform, so two phones on Happ are two rows only when they say so.
CREATE TABLE sub_clients (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_key TEXT    NOT NULL,             -- "h:<hwid>" or "a:<app>|<platform>"
    app        TEXT    NOT NULL,             -- parsed name ("Happ", "v2rayNG", …)
    version    TEXT    NOT NULL DEFAULT '',
    platform   TEXT    NOT NULL DEFAULT '',  -- "iOS", "Android", "Windows", …
    model      TEXT    NOT NULL DEFAULT '',  -- x-device-model
    ua         TEXT    NOT NULL DEFAULT '',  -- the raw User-Agent of the last fetch
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    fetches    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, client_key)
);
CREATE INDEX idx_sub_clients_last_seen ON sub_clients(last_seen);

-- How long each user was active on each protocol per day, in minutes: a minute counts
-- when the user opened a connection on that lane in it (AmneziaWG: moved traffic).
-- Xray keeps one byte counter per user across every lane, so bytes per protocol per
-- user do not exist; active minutes are the measure all lanes can give.
CREATE TABLE user_lane_daily (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day     TEXT    NOT NULL,                -- 'YYYY-MM-DD' (operator-local)
    lane    TEXT    NOT NULL,                -- Xray inbound tag, or "awg"
    minutes INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day, lane)
);
CREATE INDEX idx_user_lane_daily_day ON user_lane_daily(day);
