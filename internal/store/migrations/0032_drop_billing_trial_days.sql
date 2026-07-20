-- The trial's length is the trial plan's own period_days: a separate
-- billing_trial_days setting was a second source of truth that only the
-- registration path honoured (a manual assignment of the same plan already used
-- period_days), so the two could silently disagree. Drop the now-unused column.
-- SQLite ≥3.35 supports DROP COLUMN; the column has no index/constraint.
ALTER TABLE settings DROP COLUMN billing_trial_days;

-- traffic_daily was the one table with no retention sweep, growing forever at
-- users × nodes × days. It is now capped at model.TrafficDailyRetentionDays.
--
-- The old index carried only `day`, so every dashboard tick and every all-time
-- range walked the b-tree for the range and then went back to the table row by row
-- to fetch up/down — on a single-connection pool, with everything else queued
-- behind it. These two cover every traffic_daily query the panel makes:
--   (day, node_id, up, down)  StatsSeries, NodeTrafficTotals, the retention sweep
--   (user_id, day, up, down)  StatsSeries for one user, StatsByUser's join
-- Both replace the table lookup entirely (verified via EXPLAIN QUERY PLAN: all
-- five read as COVERING INDEX).
--
-- These live here rather than in 0031 with the rest of the feature for a boring
-- reason: 0031 has already been applied on installs in the field, and the migration
-- runner keys off the filename alone (no checksum), so anything appended to an
-- applied file silently never runs. A fresh install would get the indexes and an
-- upgraded one would not, with no error either way.
DROP INDEX IF EXISTS idx_traffic_daily_day;
CREATE INDEX IF NOT EXISTS idx_traffic_daily_day
    ON traffic_daily(day, node_id, up, down);
CREATE INDEX IF NOT EXISTS idx_traffic_daily_user_day
    ON traffic_daily(user_id, day, up, down);
