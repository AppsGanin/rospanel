-- The course of an incident (the incidents table is migration 0127): short updates
-- posted as it goes — investigating, identified, monitoring, a plain update, and the
-- closing note given when it is resolved. They go with their incident.
CREATE TABLE incident_updates (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    stage       TEXT    NOT NULL DEFAULT 'update',
    body        TEXT    NOT NULL,
    created_at  INTEGER NOT NULL
);

CREATE INDEX idx_incident_updates_incident ON incident_updates(incident_id, created_at);
