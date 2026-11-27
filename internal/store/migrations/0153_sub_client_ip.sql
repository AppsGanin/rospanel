-- The address a client fetched the subscription from. The user card joins it to the
-- connections list, so an install shows under the address it refreshed from; the
-- fetch address is a hint, not the tunnel's address (handover, CGNAT). Rows recorded
-- before this column existed stay empty and fall into the card's "no address" group.
ALTER TABLE sub_clients ADD COLUMN ip TEXT NOT NULL DEFAULT '';
