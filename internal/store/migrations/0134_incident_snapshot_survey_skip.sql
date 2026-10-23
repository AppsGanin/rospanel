-- Who an incident hit is decided when it begins, not when it is compensated: a user
-- who renewed afterwards was not using the service then. snapshot_at is when the users
-- with a running term were recorded in incident_users (0: not yet — a window that has
-- not opened, or an entry from before this migration).
ALTER TABLE incidents ADD COLUMN snapshot_at INTEGER NOT NULL DEFAULT 0;
CREATE TABLE incident_users (
    incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    user_id     INTEGER NOT NULL,
    PRIMARY KEY (incident_id, user_id)
) WITHOUT ROWID;

-- survey_skip_at: a lapse that is no churn — the term was ended by a refund — so the
-- survey neither asks about it in the bot nor on the subscription page.
ALTER TABLE users ADD COLUMN survey_skip_at INTEGER NOT NULL DEFAULT 0;
