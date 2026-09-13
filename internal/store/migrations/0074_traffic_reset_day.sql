-- The day of the month a server's monthly traffic cap starts over.
--
-- Hosting bills from the day a server was bought, not from the 1st, so a cap counted
-- over the calendar month runs out — or comes back — on the wrong day. 1–31; a day a
-- month does not have is that month's last day. 0 (the default, and what every
-- existing server has) is the 1st: exactly the window the cap was measured over
-- before this column existed. The master's lives in settings, as with 0069.
ALTER TABLE nodes ADD COLUMN traffic_reset_day INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN master_traffic_reset_day INTEGER NOT NULL DEFAULT 0;
