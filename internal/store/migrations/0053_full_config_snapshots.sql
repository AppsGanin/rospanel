-- Snapshots now capture the whole server config (protocols, ports, REALITY, routing,
-- egress, DNS, decoy, inbounds — everything on the server-settings tabs), not just the
-- routing rules. Repurpose the store: the old routing-only rows can't be restored under
-- the new format, so clear them and swap routing_json for config_json.
DELETE FROM config_snapshots;
ALTER TABLE config_snapshots DROP COLUMN routing_json;
ALTER TABLE config_snapshots ADD COLUMN config_json TEXT NOT NULL DEFAULT '';
