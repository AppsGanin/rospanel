-- One-shot commands the operator asked a node to run (self-update, geo refresh).
--
-- These lived in a map on the Manager, which meant a panel restart dropped every
-- pending one silently — and the restart that drops them is most often the panel's OWN
-- self-update, i.e. exactly the moment an operator then asks the fleet to update too.
-- POST /v1/nodes/update-all answered {"nodes":N} either way.
--
-- A row lives from the request until the node's next sync proves the response landed,
-- or until it ages out (see nodeCmdTTL). PRIMARY KEY (node_id, kind) makes re-asking
-- idempotent: it restarts the clock rather than queueing a second copy.
CREATE TABLE node_commands (
    node_id INTEGER NOT NULL,
    kind    TEXT    NOT NULL, -- 'update' | 'geo'
    at      INTEGER NOT NULL, -- unix seconds, drives the TTL
    sent    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (node_id, kind)
);
