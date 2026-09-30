-- An API key can be held to exact API methods ("GET /v1/users", "POST /v1/signup", …):
-- the ones ticked for it, comma-separated. '' keeps a key on its permissions, as
-- every key made before this reaches exactly what it did; the panel shows such a key
-- the methods those permissions open and saves the list the next time it is edited.
ALTER TABLE api_keys ADD COLUMN routes TEXT NOT NULL DEFAULT '';
