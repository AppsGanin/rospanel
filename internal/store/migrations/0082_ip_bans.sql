-- Addresses an operator banned by hand, from the addresses a user connects from
-- (core/manager_ipban.go). Dropped at the firewall of the master and of every node.
--
-- Kept apart from blocked_ips, the source policy's record: switching the policy's
-- enforcement off empties that table, and a ban an operator placed must not go with
-- it. A ban has no end — it lasts until it is lifted — so there is nothing to sweep.
-- user_id is whose address list the ban was placed from: NOT a foreign key, the ban
-- outlives the account.
CREATE TABLE ip_bans (
    ip      TEXT    PRIMARY KEY,
    user_id INTEGER NOT NULL DEFAULT 0,
    at      INTEGER NOT NULL
);
