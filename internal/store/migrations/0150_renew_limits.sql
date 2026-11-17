-- Renewal limits: the renewal of an active plan opens only renew_window_days before
-- its end, and a purchase may leave at most max_prepaid_days of paid time (never
-- below one period). 0 = no limit; a plan's 0 takes the settings' value.
ALTER TABLE settings ADD COLUMN renew_window_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN max_prepaid_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tariff_plans ADD COLUMN renew_window_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tariff_plans ADD COLUMN max_prepaid_days INTEGER NOT NULL DEFAULT 0;
