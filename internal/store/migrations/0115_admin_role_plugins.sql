-- The built-in administrator holds every permission but the admin trail, so the new
-- plugins.view / plugins.manage go to it — but only while the row is still the one
-- 0085 seeded. An owner who changed what the preset holds decided what it may do;
-- this does not second-guess them.
UPDATE admin_roles
   SET perms = 'api.manage,billing.manage,billing.view,broadcasts.manage,groups.manage,groups.view,logs.view,payments.manage,plugins.manage,plugins.view,routing.manage,routing.view,security.manage,security.view,servers.manage,servers.view,settings.manage,settings.view,stats.manage,stats.view,system.update,users.delete,users.export,users.manage,users.view,webhooks.manage'
 WHERE key = 'admin'
   AND perms = 'api.manage,billing.manage,billing.view,broadcasts.manage,groups.manage,groups.view,logs.view,payments.manage,routing.manage,routing.view,security.manage,security.view,servers.manage,servers.view,settings.manage,settings.view,stats.manage,stats.view,system.update,users.delete,users.export,users.manage,users.view,webhooks.manage';
