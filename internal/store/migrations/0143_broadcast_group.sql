-- A broadcast may be narrowed to the members of one access group, on top of its
-- audience ("active" users of the VIP group). 0 is no group. The group's name is read
-- at display time, so a renamed group shows its new name and a deleted one none.
ALTER TABLE broadcasts ADD COLUMN group_id INTEGER NOT NULL DEFAULT 0;
