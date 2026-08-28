-- Turn off default XHTTP Reality protocol if not explicitly configured with custom keys.
UPDATE settings SET reality_enabled = 0 WHERE reality_private_key = '' OR reality_dest = 'max.ru' OR reality_dest = '';
