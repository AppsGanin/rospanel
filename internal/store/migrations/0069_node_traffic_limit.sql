-- A traffic cap per server, with the period it is measured over.
--
-- Hosting sells bandwidth by the month, and a server that blows through its
-- allowance is either throttled to uselessness or billed as overage — both of which
-- the operator finds out about from the hosting panel, days late. The panel already
-- counts every byte per server; this is the threshold to compare it against, an alert
-- when it is crossed, and (optionally) dropping the server out of subscriptions until
-- the period rolls over.
--
-- 0 (the default, and what every existing server has) means no cap, which is exactly
-- the behaviour there was before. traffic_period is 'month' or 'day'; blank reads as
-- 'month', the shape hosting actually sells.
--
-- The master has no row in nodes, so its cap lives in settings under master_*, the
-- same split the placement columns use (0060).
ALTER TABLE nodes ADD COLUMN traffic_limit  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN traffic_period TEXT    NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN hide_when_over INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN master_traffic_limit  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN master_traffic_period TEXT    NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN master_hide_when_over INTEGER NOT NULL DEFAULT 0;
