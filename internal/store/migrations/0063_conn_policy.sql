-- Where a client is allowed to connect FROM (model.ConnPolicy), and the addresses
-- that policy has refused.
--
-- The policy is one JSON column like the other blocks of settings that are edited
-- as a unit (sub_rules, sub_dpi): a country mode with its list, the networks that
-- may never connect, and whether a violation is only recorded or also dropped at
-- the firewall.
--
-- blocked_ips is the panel's own record of what it dropped. The kernel set expires
-- its elements on its own, so this table is not the enforcement — it is what lets
-- the operator SEE who was cut and lift it, what survives a restart, and what the
-- nodes are handed so a client refused on one server is refused on all of them.
ALTER TABLE settings ADD COLUMN conn_policy TEXT NOT NULL DEFAULT '';
CREATE TABLE blocked_ips (
    ip      TEXT    PRIMARY KEY,
    reason  TEXT    NOT NULL DEFAULT '',   -- model.PolicyReason*
    country TEXT    NOT NULL DEFAULT '',
    asn     INTEGER NOT NULL DEFAULT 0,
    org     TEXT    NOT NULL DEFAULT '',
    user_id INTEGER NOT NULL DEFAULT 0,    -- who was connecting (0 = unknown); NOT a foreign key: the block outlives the account
    at      INTEGER NOT NULL,
    until   INTEGER NOT NULL               -- unix; the row is swept once past it
);
CREATE INDEX idx_blocked_ips_until ON blocked_ips(until);
