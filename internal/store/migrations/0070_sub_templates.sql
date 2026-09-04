-- Operator-editable subscription templates, one per format.
--
-- The generated profiles are deliberately minimal: enough routing to work anywhere
-- and nothing opinionated. An operator who wants their own DNS, rule set or group
-- layout had exactly one option before this — the mihomo template URL — and nothing
-- at all for sing-box or Xray JSON.
--
-- A template is the operator's own document with placeholders where the panel's
-- per-user parts go, so the proxies themselves stay the panel's to build. Empty (the
-- default, and every existing install) means the generated profile, unchanged.
--
-- The mihomo URL (sub_routing_mihomo) stays as it is: it fetches somebody else's
-- template, this stores your own, and an install that uses the URL keeps using it.
ALTER TABLE settings ADD COLUMN sub_tpl_clash   TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN sub_tpl_singbox TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN sub_tpl_xray    TEXT NOT NULL DEFAULT '';
