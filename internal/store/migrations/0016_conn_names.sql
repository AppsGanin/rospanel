-- Custom per-connection display names (node label in clients / on the sub page).
-- Empty ⇒ the default protocol label (VLESS-TCP-TLS, VLESS-GRPC-REALITY, …).
ALTER TABLE settings ADD COLUMN vless_name    TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN reality_name  TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN trojan_name   TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN hysteria_name TEXT NOT NULL DEFAULT '';
