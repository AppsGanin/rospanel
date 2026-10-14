-- The address a node last reached the panel from: the one its traffic leaves by, which
-- a cascade exit lets the node's service credential in from (xray.Bridge). The host
-- name alone is not enough — a cloud server behind NAT leaves by another address.
ALTER TABLE nodes ADD COLUMN seen_ip TEXT NOT NULL DEFAULT '';
