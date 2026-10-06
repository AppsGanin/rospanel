-- An account's own mailing switch and language: set over the API by an external
-- system, by the operator, or — the switch — from the bot. Mailings go to an account
-- only while neither this nor its Telegram chat's opt-out says no; the language, when
-- set, is used before the one Telegram reports.
ALTER TABLE users ADD COLUMN mailing_off INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN lang TEXT NOT NULL DEFAULT '';
-- A sign-up waiting for the operator keeps the language it came with.
ALTER TABLE registration_requests ADD COLUMN lang TEXT NOT NULL DEFAULT '';
UPDATE users SET mailing_off = 1
 WHERE tg_chat_id <> 0 AND tg_chat_id IN (SELECT chat_id FROM tg_subscribers WHERE opt_out = 1);
