-- Whether the user bot shows its broadcast on/off button (on, as it has). Off hides
-- it from the welcome and the account menu; an opt-out made before stays in force.
ALTER TABLE settings ADD COLUMN tg_mailing_switch INTEGER NOT NULL DEFAULT 1;
