-- A free-text note and a tag list on every user.
--
-- The panel knew a user by name, limits and traffic, and nothing about where they
-- came from or what the operator needs to remember about them: the reseller who
-- brought them, a support history, "pays late", a campaign. That went into the name
-- ("ivan (from telegram ad)") and then into the client apps, which show the name.
--
-- note is the operator's own text, shown only in the panel. tags is a comma-joined
-- list ("vip,reseller-a") normalised by the store — see store.encodeTags — so the
-- list can be filtered on and the same tag cannot be spelled two ways.
ALTER TABLE users ADD COLUMN note TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN tags TEXT NOT NULL DEFAULT '';
