-- Automatic messages: a rule sends its text through the user bot to everyone who
-- reaches its trigger (see model.AutoRuleTriggers), delay_hours after it, once per
-- cycle. With discount_percent a personal one-use code goes with it, valid
-- discount_days. buttons: JSON [{text, url}].
CREATE TABLE auto_rules (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT    NOT NULL,
    enabled          INTEGER NOT NULL DEFAULT 1,
    trigger          TEXT    NOT NULL,
    delay_hours      INTEGER NOT NULL,
    text             TEXT    NOT NULL,
    buttons          TEXT    NOT NULL DEFAULT '',
    discount_percent INTEGER NOT NULL DEFAULT 0,
    discount_days    INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

-- One row per message a rule sent. cycle tells repeats of the trigger apart (the
-- start of an idle spell, the end of a paid term; 0 when it happens once). user_id is
-- 0 for a chat that has no account.
CREATE TABLE auto_rule_sends (
    rule_id  INTEGER NOT NULL,
    chat_id  INTEGER NOT NULL,
    cycle    INTEGER NOT NULL DEFAULT 0,
    user_id  INTEGER NOT NULL DEFAULT 0,
    promo_id INTEGER NOT NULL DEFAULT 0,
    sent_at  INTEGER NOT NULL,
    PRIMARY KEY (rule_id, chat_id, cycle)
);
CREATE INDEX idx_auto_rule_sends_user ON auto_rule_sends(user_id) WHERE user_id <> 0;

-- A rule's personal codes, told apart from win-back ones (both are hidden from the
-- promo list through winback_user).
ALTER TABLE promo_codes ADD COLUMN auto_rule INTEGER NOT NULL DEFAULT 0;
