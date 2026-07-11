-- Scheduled backups used to exist only inside the Telegram service, so an operator
-- who never set up a bot had no automatic backups at all — even though internal/backup
-- could already produce them. These drive a local scheduler that writes archives to
-- <dataDir>/backups and keeps the newest N.
--
-- Same 5-field cron dialect and operator timezone as tg_backup_cron; empty = off.
ALTER TABLE settings ADD COLUMN local_backup_cron TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN local_backup_keep INTEGER NOT NULL DEFAULT 7;
