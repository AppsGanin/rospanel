-- Self-registration becomes a mode, not a bool. tg_user_reg_enabled stays as a
-- derived mirror (mode != 'off') so any old reader keeps working; tg_user_reg_mode
-- is the source of truth. tg_user_reg_code is the invite code for the 'invite' mode.
ALTER TABLE settings ADD COLUMN tg_user_reg_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN tg_user_reg_code TEXT NOT NULL DEFAULT '';

-- Seed the mode from the existing toggle: on ⇒ open, off ⇒ closed.
UPDATE settings SET tg_user_reg_mode = CASE WHEN tg_user_reg_enabled = 1 THEN 'open' ELSE 'off' END WHERE id = 1;
