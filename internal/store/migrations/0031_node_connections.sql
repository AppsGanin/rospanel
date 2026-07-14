-- Per-node connection transport (ports, port-hopping, WS path, REALITY port +
-- anti-replay, uTLS fingerprints, connection display names, anti-DPI) as a JSON blob.
-- Empty ⇒ the node inherits the master's transport (works out of the box); a saved
-- blob is the node's own full transport config. Protocol on/off and the REALITY
-- donor/keys are already separate per-node columns.
ALTER TABLE nodes ADD COLUMN connections_config TEXT NOT NULL DEFAULT '';
