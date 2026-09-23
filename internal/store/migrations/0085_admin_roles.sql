-- Roles become named permission sets the owner edits (model/perms.go), instead of
-- three rungs of a ladder baked into the binary.
--
-- admins.role keeps holding a role KEY, so every account keeps pointing where it did:
-- 'owner' stays the owner (who is not in this table — they hold every permission),
-- 'admin' and 'operator' become the two preset rows below, seeded with what those
-- rungs could reach — less what is now the owner's alone (the Telegram bots, backups
-- and restore; see model/perms.go). A custom role gets a generated key.
--
-- name is empty on a preset nobody renamed: the panel then shows the preset's name
-- in the viewer's language, which a stored string could not.
--
-- A permission added in a later release reaches NO existing role by itself — a role
-- is what its owner ticked. A later migration that adds a permission decides, in SQL,
-- which roles get it (typically the admin preset, to keep "admin can do everything
-- but the roster" true).
CREATE TABLE admin_roles (
    key        TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL DEFAULT '',
    perms      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

INSERT INTO admin_roles (key, perms) VALUES
    ('admin', 'api.manage,billing.manage,billing.view,broadcasts.manage,groups.manage,groups.view,logs.view,payments.manage,routing.manage,routing.view,security.manage,security.view,servers.manage,servers.view,settings.manage,settings.view,stats.manage,stats.view,system.update,users.delete,users.export,users.manage,users.view,webhooks.manage'),
    ('operator', 'groups.manage,groups.view,stats.view,users.delete,users.manage,users.view');

-- An API key may carry a role too. Empty = full access, which is what every key
-- issued so far has had — an integration keeps working through the upgrade.
ALTER TABLE api_keys ADD COLUMN role TEXT NOT NULL DEFAULT '';
