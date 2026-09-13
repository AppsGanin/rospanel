-- Hand Happ the subscription as an encrypted happ://crypt4/ link (Settings →
-- Subscriptions). Happ adds a subscription from such a link without showing its
-- address. 0 (the default) keeps the plain happ://add/ link every page has used so
-- far: an older Happ that does not know crypt4 would get a button that does nothing.
ALTER TABLE settings ADD COLUMN sub_happ_crypt INTEGER NOT NULL DEFAULT 0;
