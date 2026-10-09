-- Admin notices about plugins (one paused by its breaker, quota or an event storm)
-- are on by default, like every category before them: a paused plugin stops doing
-- its job, and the operator should hear it rather than find it.
UPDATE settings SET tg_admin_events = tg_admin_events | 4096 WHERE tg_admin_events <> -1;
