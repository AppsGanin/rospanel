-- Whether the subscription page offers its "Download Clash config" button (on, as
-- it always has). Off hides the button — and clash_url in the API's page view — while
-- Clash clients keep fetching ?format=clash as before.
ALTER TABLE settings ADD COLUMN sub_show_clash INTEGER NOT NULL DEFAULT 1;
