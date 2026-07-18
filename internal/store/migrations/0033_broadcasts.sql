-- Mass broadcasts to the user bot's audience.
--
-- The per-recipient table is the point of this design, not bookkeeping. A broadcast
-- to thousands of chats runs for minutes; holding the recipient list in memory means
-- a panel restart mid-run either loses it or, on a retry, sends everything twice.
-- With the list on disk the worker simply picks up the remaining 'pending' rows, so
-- a restart is a pause. It also gives an exact progress figure rather than an
-- estimate, makes pause/resume a status change, and makes "retry just the failures"
-- a query.
--
-- Counters are deliberately NOT stored here: sent/failed/blocked are derived from
-- broadcast_targets on read. Two places holding the same truth drift, and the drift
-- would show up as a progress bar that never reaches its total.
CREATE TABLE broadcasts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    created_by    TEXT    NOT NULL DEFAULT '', -- admin who launched it (audit trail)
    text          TEXT    NOT NULL DEFAULT '', -- HTML body
    media_kind    TEXT    NOT NULL DEFAULT '', -- '' | 'photo' | 'document'
    media_file_id TEXT    NOT NULL DEFAULT '', -- filled after the first upload, then reused
    media_name    TEXT    NOT NULL DEFAULT '', -- original filename, for the caption-less upload
    buttons_json  TEXT    NOT NULL DEFAULT '', -- [{"text":…,"url":…}] — URL buttons only
    audience      TEXT    NOT NULL DEFAULT 'all',
    status        TEXT    NOT NULL,            -- running | paused | done | cancelled
    created_at    INTEGER NOT NULL,
    started_at    INTEGER NOT NULL DEFAULT 0,
    finished_at   INTEGER NOT NULL DEFAULT 0
);

-- The audience is materialised here once, when the broadcast starts, and never
-- recomputed: a run that re-evaluated its own audience would pick up people who
-- registered halfway through and miss no one consistently.
CREATE TABLE broadcast_targets (
    broadcast_id INTEGER NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
    chat_id      INTEGER NOT NULL,
    state        TEXT    NOT NULL DEFAULT 'pending', -- pending | sent | failed | blocked
    error        TEXT    NOT NULL DEFAULT '',
    attempts     INTEGER NOT NULL DEFAULT 0,
    sent_at      INTEGER NOT NULL DEFAULT 0,
    -- One row per chat per broadcast: the primary key is what makes a resumed run
    -- unable to send twice, whatever the worker does.
    PRIMARY KEY (broadcast_id, chat_id)
);

-- Drives both the worker's "what's left" query and the progress counts.
CREATE INDEX idx_broadcast_targets_state ON broadcast_targets(broadcast_id, state);
