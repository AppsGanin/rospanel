-- Everyone who has ever opened the user bot, which is the audience a broadcast goes
-- to. Deliberately NOT users.tg_chat_id: that column only knows people who hold a
-- VPN account right now, and misses the ones a broadcast most needs to reach —
-- someone waiting on moderation, someone who mistyped an invite code, someone whose
-- account was deleted from the panel but who is still sitting in the bot.
--
-- It is also the only place "this chat blocked the bot" can live. Blocking is
-- irreversible from our side, so a broadcast has to stop addressing such chats or it
-- burns a send slot per run, forever, on someone who cannot receive it.
CREATE TABLE tg_subscribers (
    chat_id    INTEGER PRIMARY KEY,           -- Telegram user id (private chat id)
    user_id    INTEGER,                       -- NULL = never registered, or deleted since
    username   TEXT    NOT NULL DEFAULT '',
    first_name TEXT    NOT NULL DEFAULT '',
    lang       TEXT    NOT NULL DEFAULT '',   -- IETF tag from language_code; unused for now
    active     INTEGER NOT NULL DEFAULT 1,    -- 0 = blocked the bot / deactivated account
    opt_out    INTEGER NOT NULL DEFAULT 0,    -- unsubscribed from broadcasts (/mailing)
    blocked_at INTEGER NOT NULL DEFAULT 0,
    started_at INTEGER NOT NULL
);

-- The broadcast audience query filters on exactly these two flags.
CREATE INDEX idx_tg_subscribers_reachable ON tg_subscribers(active, opt_out);

-- Backfill from the accounts already linked to a chat. Without it the first
-- broadcast after an upgrade would reach only those who happened to write to the bot
-- since — which looks like a broken broadcast, not an empty table.
INSERT OR IGNORE INTO tg_subscribers (chat_id, user_id, active, started_at)
SELECT tg_chat_id, id, 1, unixepoch() FROM users WHERE tg_chat_id <> 0;
