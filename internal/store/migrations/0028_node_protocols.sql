-- Nodes no longer inherit protocols from the master — each node's protocols are its
-- own (an unset/NULL column now resolves to OFF, not "inherit the panel's toggle").
-- Freeze every existing node's previously-inherited protocols as explicit values so
-- it keeps serving exactly what it did before this change; a NULL that had meant
-- "inherit" becomes the master's current value. Fresh installs have no node rows, so
-- this is a no-op there (new nodes start with everything off).
UPDATE nodes SET
    vless_enabled    = COALESCE(vless_enabled,    (SELECT vless_enabled    FROM settings WHERE id = 1)),
    trojan_enabled   = COALESCE(trojan_enabled,   (SELECT trojan_enabled   FROM settings WHERE id = 1)),
    hysteria_enabled = COALESCE(hysteria_enabled, (SELECT hysteria_enabled FROM settings WHERE id = 1)),
    reality_enabled  = COALESCE(reality_enabled,  (SELECT reality_enabled  FROM settings WHERE id = 1));
