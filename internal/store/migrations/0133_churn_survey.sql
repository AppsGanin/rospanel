-- Why users leave: a day after a paid term lapses the user bot asks why it was not
-- renewed, and the subscription page asks the same while it stays lapsed. On only
-- when the operator turns it on (churn_survey).
ALTER TABLE settings ADD COLUMN churn_survey INTEGER NOT NULL DEFAULT 0;
-- survey_at: the lapse the bot already asked about, so it asks once per lapse.
ALTER TABLE users ADD COLUMN survey_at INTEGER NOT NULL DEFAULT 0;

-- One answer per lapse: reason is one of price, quality, not_needed, competitor,
-- other; comment what the user wrote (after "other"); source bot or page. No foreign
-- key: a deleted user's answer still counts.
CREATE TABLE churn_answers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    lapse_at   INTEGER NOT NULL,
    reason     TEXT    NOT NULL,
    comment    TEXT    NOT NULL DEFAULT '',
    source     TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (user_id, lapse_at)
);
CREATE INDEX idx_churn_answers_created ON churn_answers(created_at);
