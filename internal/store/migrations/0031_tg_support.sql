-- Support relay: the user writes to a dedicated support bot, the bot forwards into a
-- per-user topic of the operator's forum supergroup, and the admin's reply in that
-- topic is copied back. The panel stores no message history — Telegram is the store,
-- and relaying by message id is what makes screenshots and voice notes work without
-- the bot ever parsing them.
--
-- One topic per chat, not per problem: a returning user lands back in their own
-- thread with its whole history, which is what an operator wants to read before
-- answering.
CREATE TABLE tg_support_topics (
    chat_id    INTEGER PRIMARY KEY, -- Telegram user id (private chat id) of the writer
    topic_id   INTEGER NOT NULL,    -- message_thread_id in the support group
    created_at INTEGER NOT NULL
);

-- Reverse lookup: an admin's reply arrives carrying only the thread id.
CREATE UNIQUE INDEX idx_tg_support_topic ON tg_support_topics(topic_id);

-- The support bot is a THIRD bot with its own token: it has no menu, no plans and no
-- registration, so everything sent to it is unambiguously a support request. That is
-- the whole reason it's separate — inside the user bot every message would need a
-- "did they mean support or did they just tap around" decision.
ALTER TABLE settings ADD COLUMN tg_support_enabled      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN tg_support_bot_token    TEXT    NOT NULL DEFAULT '';

-- Cached @username of the support bot. The user bot renders a t.me/<username> button
-- on every menu draw, and resolving it through getMe each time would put a network
-- call on the hot path. Written by SaveTelegramSupport, which refuses to save when
-- getMe fails — an empty username here would silently hide the button.
ALTER TABLE settings ADD COLUMN tg_support_bot_username TEXT    NOT NULL DEFAULT '';

-- The forum supergroup admins answer in. 0 = not configured; support cannot be
-- enabled without it.
ALTER TABLE settings ADD COLUMN tg_support_group_id     INTEGER NOT NULL DEFAULT 0;

-- Greeting shown on /start in the support bot. Empty falls back to a built-in text.
-- Operator-editable so the promise about response time is theirs to make, not ours.
ALTER TABLE settings ADD COLUMN tg_support_greeting     TEXT    NOT NULL DEFAULT '';
