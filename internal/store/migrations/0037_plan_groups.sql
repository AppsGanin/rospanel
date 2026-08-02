-- Tariff plans grant access groups: the plan a user is on decides which connections
-- they may use, not just how much traffic and for how long.
--
-- Without this, "premium gets the fast node" had to be maintained by hand — an
-- operator confirming a payment also had to remember to tick a group in the user
-- card, and nothing ever took it back when the plan changed. The plan now carries the
-- membership and every plan assignment applies it.
CREATE TABLE plan_groups (
    plan_id  INTEGER NOT NULL REFERENCES tariff_plans(id) ON DELETE CASCADE,
    group_id INTEGER NOT NULL REFERENCES groups(id)       ON DELETE CASCADE,
    PRIMARY KEY (plan_id, group_id)
);

-- Which memberships the PLAN owns, as opposed to the ones an admin ticked by hand.
--
-- The distinction is the whole point of the flag: switching plans has to take back
-- what the previous plan granted (otherwise a user who has been through three tariffs
-- is in every group at once, and since access is the UNION of a user's groups, the
-- gate stops gating). It must NOT take back a group an operator assigned deliberately
-- — that is a decision about one person, and a payment landing at 3am is no reason to
-- undo it. So a plan write only ever touches its own rows: via_plan = 1.
--
-- Manual wins on collision. When a plan grants a group the user is ALREADY in by hand,
-- the existing row keeps via_plan = 0 and survives the next plan switch — the operator
-- put them there for a reason that outlives the tariff.
ALTER TABLE group_members ADD COLUMN via_plan INTEGER NOT NULL DEFAULT 0;
