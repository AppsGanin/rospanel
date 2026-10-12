-- The WireGuard-behind-TURN inbound is called "turn-vk" in the panel now; Xray's own
-- protocol name for it stays "wireguard" in the generated config.
UPDATE inbounds SET protocol = 'turn-vk' WHERE protocol = 'wireguard';
