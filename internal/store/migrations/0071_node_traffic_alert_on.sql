-- Switch the new "server reached its traffic limit" notification on for operators who
-- have customised their notification set.
--
-- tg_admin_events defaults to -1 (every bit, including ones that do not exist yet), so
-- an install that never touched the toggles gets the new event automatically. An
-- operator who unticked anything has a concrete mask instead, and a bit added later is
-- simply absent from it — so the people who curated their notifications are exactly
-- the people who would never hear about an overage. Same reasoning, and the same
-- statement, as 0065 used for the sign-in alert.
UPDATE settings SET tg_admin_events = tg_admin_events | 1024 WHERE tg_admin_events <> -1;
