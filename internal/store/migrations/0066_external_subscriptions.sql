-- External subscriptions (model.ExtSubscription): somebody else's servers, read
-- from their subscription and handed on to this panel's users as extra entries,
-- gated by the same access groups as the panel's own lanes.
--
-- A source is a URL fetched on every sync or the payload itself (a happ://crypt…
-- link, a base64 blob, a list of links) decoded in place; either way it is stored
-- encrypted, as is every server's link — both carry credentials that are not ours.
CREATE TABLE ext_subscriptions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL DEFAULT '',
    source        TEXT    NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_fetch_at INTEGER NOT NULL DEFAULT 0,
    last_ok_at    INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT    NOT NULL DEFAULT '',
    server_count  INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL DEFAULT (unixepoch())
);

-- key is the server's identity across reads (protocol + host + port + credential),
-- so a re-read updates a row instead of replacing it and the operator's on/off
-- choice for that server survives. A group grant names a server by id ("ext:<id>").
CREATE TABLE ext_servers (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    sub_id   INTEGER NOT NULL REFERENCES ext_subscriptions(id) ON DELETE CASCADE,
    key      TEXT    NOT NULL,
    name     TEXT    NOT NULL DEFAULT '',
    protocol TEXT    NOT NULL,
    host     TEXT    NOT NULL,
    port     INTEGER NOT NULL,
    link     TEXT    NOT NULL,
    enabled  INTEGER NOT NULL DEFAULT 1,
    seen_at  INTEGER NOT NULL DEFAULT 0,
    UNIQUE (sub_id, key)
);
CREATE INDEX idx_ext_servers_sub ON ext_servers(sub_id);
