-- Whether the subscription page shows the "individual configs" card — the raw
-- per-protocol share links, one <details> per lane.
--
-- Useful when a client can't import the subscription, and unwanted the rest of the
-- time: it hands every visitor a copyable credential per lane, which is exactly what
-- gets pasted into a group chat. Operators who run a paid service tend to want the
-- page to offer the subscription link and nothing else.
--
-- Default 1 keeps every existing install rendering exactly what it renders today.
ALTER TABLE settings ADD COLUMN sub_show_configs INTEGER NOT NULL DEFAULT 1;
