-- Where a payer lands after paying through a payment system when the flow brings no
-- address of its own (the bot, an API call without return_url). Empty: the Mini App
-- or the user's subscription page.
ALTER TABLE settings ADD COLUMN billing_return_url TEXT NOT NULL DEFAULT '';
