-- Multi-admin: a role per account, and a forced-password-change gate that follows
-- the account instead of the install.
--
-- The gate used to live on the settings singleton because there was exactly one
-- admin, so "the panel is gated" and "this admin is gated" meant the same thing.
-- With several admins they diverge: the owner hands a colleague a temporary
-- password and only that colleague is gated, while the owner keeps working. So the
-- flag moves onto admins; the settings column stays behind, unread, rather than
-- being dropped (SQLite would have to rewrite the table, and a stale column is
-- cheaper than a risky migration on a live panel).
ALTER TABLE admins ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE admins ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0;
ALTER TABLE admins ADD COLUMN last_login_at INTEGER NOT NULL DEFAULT 0;

-- The admin that already exists (there is at most one before this migration) is the
-- one who installed the panel: make them the owner and carry the install-wide gate
-- over to them, so an operator mid-first-run still lands on the password screen.
UPDATE admins
   SET role = 'owner',
       must_change_password = COALESCE(
           (SELECT must_change_password FROM settings WHERE id = 1), 0
       )
 WHERE id = (SELECT MIN(id) FROM admins);
