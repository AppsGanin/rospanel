-- The per-plan payment_url was removed from the model/API/UI (operators use the
-- payment provider or the manual-payment note instead). Drop the now-unused column.
-- SQLite ≥3.35 supports DROP COLUMN; payment_url has no index/constraint, so this
-- is a plain drop.
ALTER TABLE tariff_plans DROP COLUMN payment_url;
