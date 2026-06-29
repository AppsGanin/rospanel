-- Admin event notifications: which lifecycle/system events the admin bot pushes
-- to the authorized chats. Stored as a bitmask of the model.AdminEvent* flags;
-- the default -1 means "all categories on" (also covers any flags added later,
-- until the operator saves an explicit selection).
ALTER TABLE settings ADD COLUMN tg_admin_events INTEGER NOT NULL DEFAULT -1;
