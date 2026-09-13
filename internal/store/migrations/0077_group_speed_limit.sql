-- A speed cap on an access group (kbit/s).
--
-- 0 — the default, and what every existing group gets — sets nothing: members keep
-- the cap their tariff or their own card gives them, exactly as before this column.
-- A group that sets one takes priority over those for its members; a member of
-- several capped groups gets the highest of their caps (see store.ShapedUsers).
ALTER TABLE groups ADD COLUMN speed_limit INTEGER NOT NULL DEFAULT 0;
