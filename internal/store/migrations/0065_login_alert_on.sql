-- The sign-in alert (model.AdminEventLogin, bit 9) is on for everyone.
--
-- A fresh install stores -1 (every category on, present and future) and needs
-- nothing. An install whose operator ever saved the bot's categories stores an
-- explicit mask, written before this category existed — so it would come up off,
-- silently, on exactly the installs that use the bot most. A security alert
-- nobody asked to switch off should not need switching on.
UPDATE settings SET tg_admin_events = tg_admin_events | 512 WHERE tg_admin_events <> -1;
