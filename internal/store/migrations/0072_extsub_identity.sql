-- The device this panel claims to be when it reads somebody else's subscription.
--
-- Panels increasingly refuse a caller that does not identify a device — ours does —
-- so the fetch has to answer that question, and the answer has to be stable across
-- syncs or every refresh burns another device slot upstream until we are locked out.
--
-- Every column is an OVERRIDE. Empty (the default, and what every existing row has)
-- means the panel's own default: an id derived from the source, this build's version,
-- and a plain rospanel user agent. They exist for the case those are not what the
-- other side expects — an id the operator was given, an app name a panel whitelists,
-- or a client string that decides which format it serves back.
ALTER TABLE ext_subscriptions ADD COLUMN hwid         TEXT NOT NULL DEFAULT '';
ALTER TABLE ext_subscriptions ADD COLUMN device_os    TEXT NOT NULL DEFAULT '';
ALTER TABLE ext_subscriptions ADD COLUMN os_version   TEXT NOT NULL DEFAULT '';
ALTER TABLE ext_subscriptions ADD COLUMN device_model TEXT NOT NULL DEFAULT '';
ALTER TABLE ext_subscriptions ADD COLUMN user_agent   TEXT NOT NULL DEFAULT '';
