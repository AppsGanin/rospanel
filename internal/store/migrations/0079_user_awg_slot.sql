-- A user's place on the AmneziaWG tunnel subnet: host index awg_slot of 10.66.0.0/16,
-- the same on every server.
--
-- The address used to be derived from the user id. Ids come from AUTOINCREMENT and
-- are never reused, so a panel that had ever created 65,534 users stopped giving the
-- tunnel to every new one, however few users it had left. A slot is handed out when a
-- user's tunnel key is first needed, the lowest one free, and is freed with the user.
--
-- Users who already have a key hold a config carrying the address their id gave them;
-- they keep exactly that address (slot = id + 1, as before). 0 is no slot yet: a user
-- with no key has never been given an address, and one whose id was past the subnet
-- never had a working one.
ALTER TABLE users ADD COLUMN awg_slot INTEGER NOT NULL DEFAULT 0;

UPDATE users SET awg_slot = id + 1 WHERE wg_private_key != '' AND id >= 1 AND id + 1 <= 65534;

-- Two users on one address would each break the other's tunnel on every server.
CREATE UNIQUE INDEX idx_users_awg_slot ON users(awg_slot) WHERE awg_slot > 0;
