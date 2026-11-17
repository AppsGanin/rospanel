-- More AmneziaWG keys per user: one per device, so two devices on one account stop
-- knocking each other off the tunnel (WireGuard takes a key's endpoint from its
-- latest packet, so a shared key is one device at a time).
--
-- The user's own key (users.wg_private_key, users.awg_slot) stays and is the main
-- one; these are extra. Each has its own slot on the tunnel subnet, from the same
-- space as users.awg_slot (the store hands out the lowest one free across both).
-- hwid is set when a subscription client sending x-hwid was given the key, and is
-- what finds it again on its next fetch; '' for a key added by hand.
-- The private key is encrypted at rest like the user's own.
CREATE TABLE user_awg_devices (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT    NOT NULL DEFAULT '',
    hwid        TEXT    NOT NULL DEFAULT '',
    private_key TEXT    NOT NULL,
    slot        INTEGER NOT NULL,
    created_at  INTEGER NOT NULL
);

CREATE INDEX idx_user_awg_devices_user ON user_awg_devices(user_id);
CREATE UNIQUE INDEX idx_user_awg_devices_slot ON user_awg_devices(slot);
CREATE UNIQUE INDEX idx_user_awg_devices_hwid ON user_awg_devices(user_id, hwid) WHERE hwid <> '';
