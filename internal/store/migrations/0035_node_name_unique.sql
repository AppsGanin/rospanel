-- Enforce unique node names (case-insensitive) among live nodes at the DB level, as a
-- backstop to the app-level NodeNameTaken check: two concurrent creates/renames could
-- otherwise both pass that check and produce duplicate names, which collide as Clash
-- proxy names / sing-box tags and make a client drop a whole server. Tombstoned
-- (deleted) nodes are excluded, so a name is free to reuse after a node is deleted.
CREATE UNIQUE INDEX idx_nodes_name_live ON nodes(lower(name)) WHERE deleted_at = 0;
