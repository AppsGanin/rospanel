-- Per-node REALITY donor (masquerade SNI). Empty ⇒ inherit the panel's donor, so a
-- node works out of the box; set it to give a node its own donor (e.g. a locally-
-- appropriate site per location). REALITY keys are already per-node; this completes
-- the per-server REALITY identity.
ALTER TABLE nodes ADD COLUMN reality_dest TEXT NOT NULL DEFAULT '';
