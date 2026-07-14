-- Cert details a node reports on each sync, for its Домен tab: the issuer (≈ the ACME
-- provider that signed it) and the expiry, so the panel can show "действителен ещё N
-- дн. · выдан Let's Encrypt" like the master's domain page.
ALTER TABLE nodes ADD COLUMN cert_issuer     TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN cert_expires_at INTEGER NOT NULL DEFAULT 0;
