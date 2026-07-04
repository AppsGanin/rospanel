-- Switch the default Xray DNS from the mixed Cloudflare+Google singles to the
-- Cloudflare primary+secondary pair (1.1.1.1 / 1.0.0.1), so the "Cloudflare" preset
-- shows ticked out of the box now that presets carry a primary+secondary pair.
--
-- Only touches installs still on the untouched original default — an operator who
-- customised their DNS keeps it. Fresh DBs already get the pair from 0001's DEFAULT,
-- so this no-ops there; it exists to forward-migrate DBs created before the default
-- changed.
UPDATE settings
SET xray_dns = '1.1.1.1' || char(10) || '1.0.0.1'
WHERE id = 1 AND xray_dns = '1.1.1.1' || char(10) || '8.8.8.8';
