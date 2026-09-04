-- A tariff can carry its own quota-reset cycle ("100 GB a month, paid for a year").
--
-- Until now the cycle was derived: a free plan refilled every plan duration
-- (days:N), a paid one never refilled inside the period it was bought for. An
-- operator who wanted a monthly refill on a paid plan had to set it on every user by
-- hand — and the next purchase or plan switch silently wrote it back to "none".
--
-- '' keeps the derived behaviour for every existing plan; daily / weekly / monthly /
-- yearly are the same calendar cycles a user can be given directly.
ALTER TABLE tariff_plans ADD COLUMN reset_period TEXT NOT NULL DEFAULT '';
