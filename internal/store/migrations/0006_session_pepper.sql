-- Session HMAC pepper (mixed into session token hashes).
ALTER TABLE settings ADD COLUMN session_pepper TEXT NOT NULL DEFAULT '';
