-- The operator's own name for an external server, shown to users in place of the one
-- its source gives (empty: the source's). A re-read of the source keeps it.
ALTER TABLE ext_servers ADD COLUMN custom_name TEXT NOT NULL DEFAULT '';
