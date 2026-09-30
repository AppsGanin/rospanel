-- One trial per Telegram. Each chat that signed itself up is recorded here for good,
-- whatever later happens to the account it made — moved to another Telegram,
-- deleted, unlinked — so signing up again from it gives an account without a trial.
-- Kept apart from users: an account's chat and its "previous chat" pointer both move
-- with the account, and a trial belongs to the Telegram that took it.
CREATE TABLE tg_trial_chats (
    chat_id INTEGER PRIMARY KEY,
    at      INTEGER NOT NULL
);
-- The chats of every account that already had its trial: the one it is on, and the
-- one it was last unlinked from.
INSERT OR IGNORE INTO tg_trial_chats (chat_id, at)
    SELECT tg_chat_id, unixepoch() FROM users WHERE trial_used = 1 AND tg_chat_id <> 0;
INSERT OR IGNORE INTO tg_trial_chats (chat_id, at)
    SELECT tg_prev_chat_id, unixepoch() FROM users WHERE trial_used = 1 AND tg_prev_chat_id <> 0;
