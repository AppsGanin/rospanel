-- More figures on the load charts: disk use, network throughput and users online.
--
-- ext counts the samples taken since, ext_stats those of them that carried host
-- stats (disk and network; online is the panel's own count and is in every one).
-- Rows written before this migration have none, and their zeros must read as "not
-- measured", not as an idle server.
-- Disk is percent of the data disk in use; rx/tx are bytes per second, averaged
-- over the minute before each sample; online is distinct users seen on the server.
ALTER TABLE server_load ADD COLUMN ext        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE server_load ADD COLUMN ext_stats  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE server_load ADD COLUMN disk_sum   REAL    NOT NULL DEFAULT 0;
ALTER TABLE server_load ADD COLUMN disk_max   REAL    NOT NULL DEFAULT 0;
ALTER TABLE server_load ADD COLUMN rx_sum     REAL    NOT NULL DEFAULT 0;
ALTER TABLE server_load ADD COLUMN rx_max     REAL    NOT NULL DEFAULT 0;
ALTER TABLE server_load ADD COLUMN tx_sum     REAL    NOT NULL DEFAULT 0;
ALTER TABLE server_load ADD COLUMN tx_max     REAL    NOT NULL DEFAULT 0;
ALTER TABLE server_load ADD COLUMN online_sum INTEGER NOT NULL DEFAULT 0;
ALTER TABLE server_load ADD COLUMN online_max INTEGER NOT NULL DEFAULT 0;
