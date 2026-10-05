-- The operator's own subscription page: a browser opening a subscription link is
-- sent there ({token} becomes the user's token) instead of the panel's page. Empty
-- keeps the panel's page. Apps fetching the subscription are not affected.
ALTER TABLE settings ADD COLUMN sub_page_url TEXT NOT NULL DEFAULT '';
