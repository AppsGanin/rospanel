-- Client-side DPI evasion handed out by the subscription (model.SubDPI): TLS
-- ClientHello fragmentation and noise for Xray-core clients through the Xray JSON
-- subscription format, record fragmentation for sing-box. One JSON column, like
-- sub_rules: the settings form saves the block as a unit, and nine columns for one
-- feature would be nine places a later change has to touch.
ALTER TABLE settings ADD COLUMN sub_dpi TEXT NOT NULL DEFAULT '';
