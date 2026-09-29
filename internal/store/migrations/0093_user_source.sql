-- Where a user came from: the tag a /start link carried (t.me/<bot>?start=<tag>),
-- remembered on the chat at first contact and copied to the account it registers.
-- The first tag wins; '' = none. The funnel groups users by it.
ALTER TABLE tg_subscribers ADD COLUMN source TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN source TEXT NOT NULL DEFAULT '';
