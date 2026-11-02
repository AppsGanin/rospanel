-- The operator's own picture for manual payment — a QR code of their СБП/merchant
-- account, a screenshot of the card or wallet page — shown wherever the manual
-- instructions are: on the subscription page, in the user bot's message and as
-- image_url in the /v1 order response.
--
-- A row in the database rather than a file under the data directory: three readers
-- need the bytes and two of them (the core manager behind the bots and the API) never
-- learn where that directory is. One row, always id = 1 — this is a setting, not a
-- gallery.
--
-- version is a hash of the bytes, used as the ?v= in the URL the page hands the
-- browser: a replaced picture gets a new URL, so a long cache is safe and an old copy
-- can never outlive the upload.
CREATE TABLE manual_pay_image (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    image      BLOB    NOT NULL,
    version    TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);
