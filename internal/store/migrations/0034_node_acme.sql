-- Per-node ACME: each node can have its OWN CA provider (Let's Encrypt / ZeroSSL) and
-- e-mail for cert issuance, so its Домен tab matches the master's. Empty ⇒ inherit the
-- panel's. ZeroSSL EAB credentials are fetched by the panel and pushed to the node;
-- the HMAC is encrypted at rest.
ALTER TABLE nodes ADD COLUMN acme_email        TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN acme_provider     TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN zerossl_eab_kid   TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN zerossl_eab_hmac  TEXT NOT NULL DEFAULT '';
