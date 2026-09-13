-- A term that starts on the user's first connection (Marzban's "on hold", 3x-ui's
-- "start after first use").
--
-- hold_seconds is the length of that term. While it is above 0 the user has no
-- expiry date (expire_at = 0); the first connection the panel records sets
-- expire_at to that moment plus hold_seconds and puts this back to 0, in the same
-- transaction. 0 — the default, and what every existing user gets — is no pending
-- term, which is exactly how every user behaved before this column existed.
ALTER TABLE users ADD COLUMN hold_seconds INTEGER NOT NULL DEFAULT 0;

-- The first-connection check reads only the users with a pending term, on every
-- flush of the access log. They are few; this keeps it from reading everyone else.
CREATE INDEX idx_users_held ON users(id) WHERE hold_seconds > 0;
