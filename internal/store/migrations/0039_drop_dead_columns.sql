-- Drop the columns nothing has read for several releases.
--
-- Two features left theirs behind on purpose, and both reasons have expired:
--
--   trojan_* / ws_path (0034, Trojan-WS retired) were kept so a rollback to the
--   previous binary would still find the schema it expected. That binary is many
--   releases back now; a rollback that far is a restore-from-backup, not a downgrade.
--
--   yookassa_* / cryptobot_* (0027, providers became rows in payment_providers) were
--   kept because credentials are re-entered through the new per-provider form. An
--   operator who upgraded but never went back to that form still has their old keys
--   only here — so this migration MOVES them before dropping, rather than deleting
--   somebody's live shop secret to tidy up a schema.
--
-- The rescue writes plaintext JSON into payment_providers.config. That is the shape
-- the app already handles: ReencryptSensitiveFields wraps a plaintext config in the
-- at-rest envelope at the next start (the same path a restore from an old backup
-- takes), and the read side passes plaintext through. SQLite cannot encrypt, and a
-- migration that skipped the copy to avoid the plaintext window would lose the data
-- outright.
--
-- Only for providers with no row yet: a configuration the operator has already
-- entered in the new form is the current one and must not be overwritten by a stale
-- column. json_quote keeps a key containing a quote or a backslash from producing
-- invalid JSON.
INSERT INTO payment_providers (key, enabled, config)
SELECT 'yookassa', yookassa_enabled,
       '{"shop_id":' || json_quote(yookassa_shop_id) ||
       ',"secret_key":' || json_quote(yookassa_secret_key) ||
       ',"test":' || CASE WHEN yookassa_test = 1 THEN '"1"' ELSE '""' END || '}'
  FROM settings
 WHERE id = 1
   AND (yookassa_shop_id <> '' OR yookassa_secret_key <> '')
   AND NOT EXISTS (SELECT 1 FROM payment_providers WHERE key = 'yookassa');

INSERT INTO payment_providers (key, enabled, config)
SELECT 'cryptobot', cryptobot_enabled,
       '{"token":' || json_quote(cryptobot_token) ||
       ',"testnet":' || CASE WHEN cryptobot_testnet = 1 THEN '"1"' ELSE '""' END || '}'
  FROM settings
 WHERE id = 1
   AND cryptobot_token <> ''
   AND NOT EXISTS (SELECT 1 FROM payment_providers WHERE key = 'cryptobot');

-- The retired Trojan-WS lane.
ALTER TABLE settings DROP COLUMN trojan_enabled;
ALTER TABLE settings DROP COLUMN trojan_port;
ALTER TABLE settings DROP COLUMN trojan_fp;
ALTER TABLE settings DROP COLUMN trojan_name;
ALTER TABLE settings DROP COLUMN ws_path;
ALTER TABLE nodes    DROP COLUMN trojan_enabled;

-- The pre-registry payment settings, now carried in payment_providers.
ALTER TABLE settings DROP COLUMN yookassa_enabled;
ALTER TABLE settings DROP COLUMN yookassa_shop_id;
ALTER TABLE settings DROP COLUMN yookassa_secret_key;
ALTER TABLE settings DROP COLUMN yookassa_test;
ALTER TABLE settings DROP COLUMN cryptobot_enabled;
ALTER TABLE settings DROP COLUMN cryptobot_token;
ALTER TABLE settings DROP COLUMN cryptobot_testnet;
