-- Time given back for an incident or a maintenance window: once it is over the
-- operator can add its length (or what they choose) to the subscriptions of the users
-- it hit. compensated_at is when that was done (0: not yet), so it is done once;
-- compensated_seconds is how much each user got, compensated_users how many got it.
ALTER TABLE incidents ADD COLUMN compensated_at      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE incidents ADD COLUMN compensated_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE incidents ADD COLUMN compensated_users   INTEGER NOT NULL DEFAULT 0;
