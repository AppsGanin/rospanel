-- Manual payment becomes a payment method of its own, switched on and off like a
-- provider and carrying its own pay-button label (the way every provider has one
-- through payments.DisplayNameKey), instead of the fallback that appeared only while
-- no provider was enabled.
--
-- Existing installs keep what they had: the switch starts on exactly where the
-- fallback was in effect — no provider row enabled. An install whose only enabled
-- provider is half-configured was serving manual orders too (the panel offers a
-- provider only once every required field is filled, which SQL cannot see here), so
-- its operator turns the switch back on themselves.
ALTER TABLE settings ADD COLUMN billing_manual_enabled INTEGER NOT NULL DEFAULT 0;

-- Empty falls back to the dictionary's wording for the method.
ALTER TABLE settings ADD COLUMN billing_manual_label TEXT NOT NULL DEFAULT '';

UPDATE settings
   SET billing_manual_enabled = 1
 WHERE NOT EXISTS (SELECT 1 FROM payment_providers WHERE enabled = 1);
