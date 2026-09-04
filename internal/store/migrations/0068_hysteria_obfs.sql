-- Salamander obfuscation for the built-in Hysteria2 lane.
--
-- Hysteria2 is QUIC, and a QUIC handshake is the one thing a DPI box can recognise
-- about the lane before a single byte of traffic flows. Salamander XORs every
-- datagram with BLAKE2b-256(psk‖salt), so the wire carries no recognisable header.
-- The key is shared: it goes into every share link and profile the panel emits.
--
-- Empty (the default, and what every existing install has) means no obfuscation —
-- turning it on changes what clients must send, so it can never be retroactive.
ALTER TABLE settings ADD COLUMN hysteria_obfs TEXT NOT NULL DEFAULT '';
