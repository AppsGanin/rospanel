-- Telegram support relay + mass broadcasts (issue #29).

-- ---------------------------------------------------------------------------
-- Support relay
-- ---------------------------------------------------------------------------
-- A user writes to a dedicated support bot; the bot forwards into a per-user topic
-- of the operator's forum supergroup, and an admin's reply in that topic is copied
-- back. No message history is stored — Telegram is the store, and relaying by
-- message id is what lets screenshots and voice notes pass through without the bot
-- parsing a single attachment.
--
-- One topic per chat, not per problem: a returning user lands back in their own
-- thread with its whole history, which is what an operator wants to read before
-- answering.
--
-- group_id is on the row because a thread id is a message id, and message ids are
-- unique only WITHIN a chat. Keyed globally, a mapping made in group A stayed
-- addressable after support was pointed at group B: an admin writing in B's topic 7
-- had it delivered to whoever owned topic 7 in A, and that user's next message was
-- forwarded into a stranger's thread. Carrying the group makes the question local —
-- a mapping matches only inside the group that issued it, so no group change needs
-- special handling and nothing has to be reset (a reset had to be exactly right on
-- every path, and each way of being wrong either leaked messages across customers or
-- orphaned live conversations Telegram gives no way to find again).
CREATE TABLE tg_support_topics (
    chat_id    INTEGER PRIMARY KEY, -- Telegram user id (private chat id) of the writer
    group_id   INTEGER NOT NULL DEFAULT 0,
    topic_id   INTEGER NOT NULL,    -- message_thread_id in the support group
    created_at INTEGER NOT NULL
);

-- Reverse lookup for an admin's reply, which arrives carrying only the thread id.
-- Unique per group, not globally: thread ids repeat across groups, and enforcing
-- more than this wedged a new user out of support whenever a fresh group reused an
-- id an old one had handed out.
CREATE UNIQUE INDEX idx_tg_support_topic ON tg_support_topics(group_id, topic_id);

-- Groups the support bot has been added to, so the operator picks one from a list
-- instead of hunting a numeric chat id out of a Telegram Web URL (remembering the
-- -100 prefix) or letting a stranger's id-printing bot into the group where customer
-- conversations will live.
--
-- These are CANDIDATES, never an automatic choice. The support bot is reachable by
-- @username, so anyone can add it to a group and land a row here; applying one
-- without the operator saying so would let a stranger redirect every support
-- conversation to a chat they control.
CREATE TABLE tg_support_groups (
    chat_id  INTEGER PRIMARY KEY,
    title    TEXT    NOT NULL DEFAULT '',
    is_forum INTEGER NOT NULL DEFAULT 0, -- topics enabled (required to be usable)
    is_admin INTEGER NOT NULL DEFAULT 0, -- bot is an admin (required to see replies)
    seen_at  INTEGER NOT NULL,
    -- When the bot's rights were last actually checked. Kept apart from seen_at,
    -- which moves on every message: debouncing the check against "last seen" meant a
    -- busy group was never re-checked at all (always recent) while a quiet one was
    -- checked on every single message (never recent).
    rights_at INTEGER NOT NULL DEFAULT 0
);

-- The support bot is a THIRD bot with its own token: it has no menu, no plans and no
-- registration, so everything sent to it is unambiguously a support request. That is
-- the whole reason it is separate — inside the user bot every message would need a
-- "did they mean support, or are they tapping around?" decision.
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

-- ---------------------------------------------------------------------------
-- Broadcast audience
-- ---------------------------------------------------------------------------
-- Everyone who has ever opened the user bot. Deliberately NOT users.tg_chat_id: that
-- column only knows people who hold a VPN account right now, and misses the ones a
-- broadcast most needs to reach — someone waiting on moderation, someone who mistyped
-- an invite code, someone whose account was deleted but who is still in the bot.
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

-- Backfill from the accounts already linked to a chat. Without it the first broadcast
-- after an upgrade would reach only those who happened to write to the bot since —
-- which reads as a broken feature, not an empty table.
INSERT OR IGNORE INTO tg_subscribers (chat_id, user_id, active, started_at)
SELECT tg_chat_id, id, 1, unixepoch() FROM users WHERE tg_chat_id <> 0;

-- ---------------------------------------------------------------------------
-- Broadcasts
-- ---------------------------------------------------------------------------
-- The per-recipient table is the point of this design, not bookkeeping. A broadcast
-- to thousands of chats runs for minutes; holding the recipient list in memory means
-- a panel restart mid-run either loses it or, on a retry, sends everything twice.
-- With the list on disk the worker simply picks up the remaining 'pending' rows, so a
-- restart is a pause. It also gives an exact progress figure rather than an estimate,
-- makes pause/resume a status change, and makes "retry just the failures" a query.
--
-- Counters are deliberately NOT stored here: sent/failed/blocked/skipped are derived
-- from broadcast_targets on read. Two places holding the same truth drift, and the
-- drift would show up as a progress bar that never reaches its total.
CREATE TABLE broadcasts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    created_by    TEXT    NOT NULL DEFAULT '', -- admin who launched it (audit trail)
    text          TEXT    NOT NULL DEFAULT '', -- HTML body
    media_kind    TEXT    NOT NULL DEFAULT '', -- '' | 'photo' | 'document'
    media_file_id TEXT    NOT NULL DEFAULT '', -- filled after the first upload, then reused
    media_name    TEXT    NOT NULL DEFAULT '', -- original filename, for the upload
    buttons_json  TEXT    NOT NULL DEFAULT '', -- [{"text":…,"url":…}] — URL buttons only
    audience      TEXT    NOT NULL DEFAULT 'all',
    status        TEXT    NOT NULL,            -- running | paused | done | cancelled
    created_at    INTEGER NOT NULL,
    started_at    INTEGER NOT NULL DEFAULT 0,
    finished_at   INTEGER NOT NULL DEFAULT 0
);

-- The audience is materialised here once, when the broadcast starts, and never
-- recomputed: a run that re-evaluated its own audience would pick up people who
-- registered halfway through and move the total its progress is measured against.
CREATE TABLE broadcast_targets (
    broadcast_id INTEGER NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
    chat_id      INTEGER NOT NULL,
    -- pending | sent | failed | blocked | skipped. 'skipped' is someone who
    -- unsubscribed after the snapshot was taken: the snapshot fixes who is in scope,
    -- but it must not override a decision the bot has since confirmed to them.
    state        TEXT    NOT NULL DEFAULT 'pending',
    error        TEXT    NOT NULL DEFAULT '',
    attempts     INTEGER NOT NULL DEFAULT 0,
    sent_at      INTEGER NOT NULL DEFAULT 0,
    -- One row per chat per broadcast: this is what makes a resumed run unable to
    -- send twice, whatever the worker does.
    PRIMARY KEY (broadcast_id, chat_id)
);

-- Drives both the worker's "what's left" query and the progress counts.
CREATE INDEX idx_broadcast_targets_state ON broadcast_targets(broadcast_id, state);
