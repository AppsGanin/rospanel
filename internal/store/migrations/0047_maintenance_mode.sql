-- Maintenance mode: when on, the public surfaces (subscription page, status page,
-- decoy fallback) answer with a "temporarily unavailable" page while the panel, the
-- external API, node sync and the VPN tunnels keep working — so the operator can
-- still sign in to switch it off and users' existing connections are untouched.
ALTER TABLE settings ADD COLUMN maintenance_mode INTEGER NOT NULL DEFAULT 0;
