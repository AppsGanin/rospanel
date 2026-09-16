-- A revision of the settings row that no writer has to remember to bump: a trigger does
-- it on every insert, update and delete. The store keeps one decoded copy of the
-- settings and, with a one-row read of this table, knows when that copy is out of date.
CREATE TABLE settings_rev (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    v  INTEGER NOT NULL
);
INSERT INTO settings_rev (id, v) VALUES (1, 0);

CREATE TRIGGER settings_rev_on_update AFTER UPDATE ON settings
BEGIN
    UPDATE settings_rev SET v = v + 1 WHERE id = 1;
END;

CREATE TRIGGER settings_rev_on_insert AFTER INSERT ON settings
BEGIN
    UPDATE settings_rev SET v = v + 1 WHERE id = 1;
END;

CREATE TRIGGER settings_rev_on_delete AFTER DELETE ON settings
BEGIN
    UPDATE settings_rev SET v = v + 1 WHERE id = 1;
END;
