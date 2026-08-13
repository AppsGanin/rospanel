-- Device identity for the per-user device cap.
--
-- The count the panel has always shown comes from `connections`: distinct source IPs
-- seen inside DeviceOnlineWindow. That answers "how many places is this account
-- connected from right now", and on real networks it is wrong in both directions — a
-- phone crossing Wi-Fi→LTE counts twice, while a household behind one CGNAT address
-- counts once no matter how many devices hide there. Neither error is rare, and both
-- land on the operator as a support ticket.
--
-- Clients that follow the subscription-header convention (Happ, v2RayTun, …) send a
-- stable per-install id in x-hwid when they fetch the subscription, which names the
-- DEVICE instead of the path its packets took. A row here is one such device that
-- fetched and was admitted.
--
-- Rows survive until the operator unbinds them or hwid_ttl_days of silence passes, so
-- the cap counts installs rather than sessions. That is the point: waiting two minutes
-- must not free a slot the way it does for the IP count, or the cap means nothing.
CREATE TABLE devices (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hwid       TEXT    NOT NULL,
    os         TEXT    NOT NULL DEFAULT '',   -- x-device-os
    os_version TEXT    NOT NULL DEFAULT '',   -- x-ver-os
    model      TEXT    NOT NULL DEFAULT '',   -- x-device-model
    app        TEXT    NOT NULL DEFAULT '',   -- User-Agent of the fetching client
    ip         TEXT    NOT NULL DEFAULT '',   -- source address of the last fetch
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL,
    PRIMARY KEY (user_id, hwid)
);

-- The TTL sweep ranges over last_seen across every user; the primary key can't serve
-- that, and carrying user_id keeps the index covering. Same shape, and the same
-- reasoning, as idx_connections_last_seen.
CREATE INDEX idx_devices_last_seen ON devices(last_seen, user_id);

-- Off by default: switching it on changes who can connect, and an operator upgrading
-- the panel did not ask for that.
ALTER TABLE settings ADD COLUMN hwid_enabled INTEGER NOT NULL DEFAULT 0;

-- Whether a client that sends no x-hwid is refused the subscription.
--
-- Default 1, because the alternative makes the cap optional for whoever wants to dodge
-- it: a user who has run out of slots only has to switch to a client that sends no id.
-- It is a separate switch all the same — turning it off keeps clients like v2rayN and
-- Clash working (counted the old way, by address) while devices that DO identify
-- themselves are still bound and capped, which is the right trade for an operator whose
-- users are not all on Happ.
ALTER TABLE settings ADD COLUMN hwid_require INTEGER NOT NULL DEFAULT 1;

-- Cap applied to users whose own device_limit is 0 (unlimited). 0 keeps them
-- unlimited, which is what "no limit set" has always meant.
ALTER TABLE settings ADD COLUMN hwid_fallback_limit INTEGER NOT NULL DEFAULT 0;

-- Days of silence after which a device is forgotten and its slot returns. 0 = never.
ALTER TABLE settings ADD COLUMN hwid_ttl_days INTEGER NOT NULL DEFAULT 30;
