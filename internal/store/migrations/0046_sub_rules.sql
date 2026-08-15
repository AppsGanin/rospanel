-- Subscription response rules: an ordered JSON list of operator rules evaluated
-- before automatic format detection (force a format, or block a client). Stored as
-- one blob since the set is small and edited whole. Empty ⇒ no rules, auto-detect.
ALTER TABLE settings ADD COLUMN sub_rules TEXT NOT NULL DEFAULT '';
