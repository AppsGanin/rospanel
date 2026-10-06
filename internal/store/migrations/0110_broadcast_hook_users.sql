-- How many accounts a broadcast went to through the external system (broadcast.sent):
-- those the bot does not reach. Shown beside the bot's own progress.
ALTER TABLE broadcasts ADD COLUMN hook_users INTEGER NOT NULL DEFAULT 0;
