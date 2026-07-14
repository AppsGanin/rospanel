-- Per-node egress backends (proxy lanes already live in nodes.routing_config).
-- WARP: each node needs its OWN Cloudflare WARP registration (a shared WireGuard
-- identity across servers is unsafe), so the keys/endpoint/addresses live per node.
-- Opera: the node's agent runs the opera-proxy helper locally; the country is stored.
-- All default off — a node has no egress config until the operator sets one.
ALTER TABLE nodes ADD COLUMN warp_enabled     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN warp_private_key TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN warp_public_key  TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN warp_endpoint    TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN warp_address_v4  TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN warp_address_v6  TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN warp_reserved    TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN opera_enabled    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN opera_country    TEXT    NOT NULL DEFAULT '';
