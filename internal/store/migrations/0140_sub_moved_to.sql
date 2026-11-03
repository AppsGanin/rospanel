-- The URL a client should follow when the subscription moves to another host.
-- Empty means the panel has not moved and the header is omitted.
ALTER TABLE settings ADD COLUMN sub_moved_to TEXT NOT NULL DEFAULT '';
