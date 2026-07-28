-- Localisation: the one stored setting the RU/EN work needed, and the rename of
-- the shipped plan names it made necessary.

-- The language the admin bot speaks: its menus, user cards and the push
-- notifications the panel sends to the linked admin chats.
--
-- Panel-wide rather than per-admin on purpose. The client and support bots take
-- each person's language from Telegram, which works because every message they
-- send answers an incoming one. The admin bot also pushes unprompted alerts (Xray
-- crashed, a certificate renewed, a payment landed) where there is no update to
-- read a language from — so it needs a stored preference, and one setting on the
-- page the bot is already configured from beats a table keyed by admin chat.
--
-- Empty means "the panel default" (Russian), which is what every existing install
-- was already getting.
ALTER TABLE settings ADD COLUMN tg_lang TEXT NOT NULL DEFAULT '';

-- The three stock plans were seeded with Russian names (0007). They are display
-- names shown to every user, in a panel that is now bilingual, so the stock ones
-- are English like the rest of the shipped defaults.
--
-- Guarded on the original name, not just the slug: an operator who renamed their
-- plan chose that name, and a migration has no business overwriting it. Only a
-- plan still carrying the seeded wording is touched.
UPDATE tariff_plans SET name = 'Free'     WHERE slug = 'free'  AND name = 'Бесплатный';
UPDATE tariff_plans SET name = 'Trial'    WHERE slug = 'trial' AND name = 'Пробный';
UPDATE tariff_plans SET name = 'Standard' WHERE slug = 'month' AND name = 'Стандарт';
