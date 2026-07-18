-- Groups the support bot has been added to, so the operator picks one from a list
-- instead of hunting down a numeric chat id.
--
-- Finding a supergroup id by hand means either reading it out of a Telegram Web URL
-- and remembering to prepend "-100", or adding a stranger's id-printing bot to the
-- group where your customers' support conversations will live. The bot already
-- receives its own membership events; this table is just somewhere to keep them.
--
-- These are CANDIDATES, never an automatic choice. The support bot is reachable by
-- @username, so anyone can add it to a group and land a row here — applying one
-- without the operator saying so would let a stranger redirect every support
-- conversation to a chat they control.
CREATE TABLE tg_support_groups (
    chat_id  INTEGER PRIMARY KEY,
    title    TEXT    NOT NULL DEFAULT '',
    is_forum INTEGER NOT NULL DEFAULT 0, -- topics enabled (required to be usable)
    is_admin INTEGER NOT NULL DEFAULT 0, -- bot is an admin (required to see replies)
    seen_at  INTEGER NOT NULL
);
