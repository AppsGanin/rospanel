-- user.expiring now goes out at fixed stages before a term ends — 14, 7, 3 and 1
-- days — each once per term: hook_expire_stage is the last stage sent for the term
-- hook_expire_at holds (0 = none yet; a renewal moves expire_at and starts over).
ALTER TABLE users ADD COLUMN hook_expire_stage INTEGER NOT NULL DEFAULT 0;
