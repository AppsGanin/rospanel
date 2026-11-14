-- VLESS-XHTTP-TLS on the VLESS-TCP-TLS port (internal/xhttpfront): requests on its
-- secret path go from the decoy behind :443 to Xray's internal xhttp-in inbound, on each
-- user's UUID. The lane's switch, display name and path are new, on by default; the
-- path is one random segment for the whole fleet, made at boot (Store.EnsureXHTTPPath).
-- A node's switch is on its row and serves_xhttp is what its agent last said.
ALTER TABLE settings ADD COLUMN xhttp_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE settings ADD COLUMN xhttp_name    TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN xhttp_path    TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN xhttp_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE nodes ADD COLUMN serves_xhttp  INTEGER NOT NULL DEFAULT 0;
