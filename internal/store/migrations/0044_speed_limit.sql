-- Per-user speed cap, in kilobits per second. 0 = unlimited.
--
-- Enforced below Xray, by the kernel's scheduler on the addresses the user is
-- currently connected from (see internal/shaper for why it cannot live inside
-- Xray, and what the address-keyed approach does and doesn't guarantee).
--
-- Kilobits rather than megabits because operators sell "512 Kbps" plans as readily
-- as "50 Mbps" ones, and an integer of Mbit cannot express the first.
ALTER TABLE users ADD COLUMN speed_limit INTEGER NOT NULL DEFAULT 0;

-- The plan carries it too, next to the traffic and device caps: a tariff that
-- promises a speed has to be able to set one, and plan assignment already
-- overwrites the other two limits from the plan (planWriteFor).
ALTER TABLE tariff_plans ADD COLUMN speed_limit INTEGER NOT NULL DEFAULT 0;
