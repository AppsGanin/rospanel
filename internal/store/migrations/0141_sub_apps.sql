-- The subscription page's "open in app" buttons, as the operator arranges them.
--   sub_apps         the order: client keys joined by commas, "-key" for one switched
--                    off; '' = every client in the stock order
--   sub_custom_apps  the operator's own clients, JSON [{id, name, platforms, link}],
--                    placed in sub_apps as "x" + id
--   sub_app_links    each client's download buttons, JSON {key: {platform: [{label,
--                    url}]}}; a stock client not in it keeps its built-in ones
--   sub_wizard_apps  the connect wizard's client per platform, JSON {platform: key};
--                    a platform not in it gets Happ
ALTER TABLE settings ADD COLUMN sub_apps TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN sub_custom_apps TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN sub_app_links TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN sub_wizard_apps TEXT NOT NULL DEFAULT '';
