-- Panel branding: custom display name + colour theme shown in the UI and on the
-- subscription page. panel_theme is a JSON object of hex colours
-- {accent,text,muted,bg,surface}; empty/absent keys fall back to the built-in
-- «РосПанель» defaults. A custom logo, when set, is stored as a file under
-- <dataDir>/branding/logo (not in the DB). All of this rides along in the backup.
ALTER TABLE settings ADD COLUMN panel_name  TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN panel_theme TEXT NOT NULL DEFAULT '';
