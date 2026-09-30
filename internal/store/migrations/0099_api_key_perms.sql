-- An API key carries its own permissions instead of an admin role. A key is one
-- integration — a bot, a website, a dashboard — and what it may do is decided for
-- it alone; a role made up for every integration crowded the roles meant for people.
--
-- Every key keeps exactly the reach it had: its role's permissions are copied onto
-- it (a revoked one too — the record of what it could do), and a key with no role
-- stays full access. The role column is then cleared, so no key holds a role and a
-- role is the admins' alone.
ALTER TABLE api_keys ADD COLUMN perms TEXT NOT NULL DEFAULT '';
ALTER TABLE api_keys ADD COLUMN full_access INTEGER NOT NULL DEFAULT 0;
UPDATE api_keys SET full_access = 1 WHERE role = '';
UPDATE api_keys
   SET perms = COALESCE((SELECT r.perms FROM admin_roles r WHERE r.key = api_keys.role), '')
 WHERE role <> '';
UPDATE api_keys SET role = '';
