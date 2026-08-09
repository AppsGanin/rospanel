-- Two-factor authentication for the panel login (TOTP, RFC 6238).
--
-- Per admin and opt-in: whoever wants a second factor turns it on for themselves, and
-- nobody — not even the owner — can read somebody else's secret through the panel.
-- The way back in after a lost phone is `rospanel totp reset <login>` on the server,
-- not a recovery code the operator would have stored next to their password anyway;
-- whoever can run that command already holds the database and the key that decrypts it.
--
-- totp_secret is the shared secret, encrypted at rest like every other secret here,
-- and non-empty ONLY once the admin has proved with a live code that their app is
-- actually set up. Enabling it any earlier is how an operator locks themselves out of
-- their own panel with a QR code they never managed to scan.
ALTER TABLE admins ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';

-- The secret being set up but not yet confirmed. A column rather than server memory
-- so the flow survives a page reload and a panel restart: the operator scans the QR,
-- fumbles for their phone, and the code they finally type still has something to
-- check against.
ALTER TABLE admins ADD COLUMN totp_pending TEXT NOT NULL DEFAULT '';

-- The last time step accepted for this admin. This is what makes a code ONE-time:
-- without it a code stays valid for the remainder of its 30-second window, so one
-- read over a shoulder, out of a screenshot, or from a logging proxy is enough to
-- reuse it.
ALTER TABLE admins ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;
