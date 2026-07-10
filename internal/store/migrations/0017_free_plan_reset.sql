-- Free plans now refill the traffic quota every "срок действия" (a rolling N-day
-- cycle anchored at the last reset), not on a fixed calendar month. Two data
-- fixes so existing installs behave the same as fresh ones after this change:
--
-- 1. Free plans (price 0) historically had period_days forced to 0 (no срок), so
--    under the new rule they'd never reset. Give them a 30-day default cycle —
--    the closest match to the old monthly reset. The trial plan (period_days 3)
--    and any paid plan are untouched: this only hits price-0/period-0 rows, which
--    can only be the free fallback plan.
UPDATE tariff_plans
SET period_days = 30
WHERE price_rub = 0 AND period_days = 0;

-- 2. Users currently on the calendar "monthly" reset that belong to a free plan
--    move to the rolling "days:N" cycle matching their plan's срок действия
--    (30 after step 1, or whatever the operator set). Old code only assigned
--    "monthly" to free-plan users; any manual monthly reset on a non-free user is
--    intentionally left as calendar-monthly.
UPDATE users
SET reset_period = 'days:' || (
    SELECT period_days FROM tariff_plans WHERE tariff_plans.id = users.plan_id
)
WHERE reset_period = 'monthly'
  AND plan_id IN (SELECT id FROM tariff_plans WHERE price_rub = 0 AND period_days > 0);
