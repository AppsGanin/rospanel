-- {flag} and {country} are no longer connection-name variables (model/nametmpl.go).
-- A name still carrying one would now show the braces verbatim in every client, so
-- they are taken out of the names that hold them: "{flag} VLESS ({left})" becomes
-- "VLESS ({left})".
--
-- A name is left as it is when taking them out would produce one the name validation
-- refuses: an empty custom inbound name (an empty lane name is that lane's default and
-- fine), a reserved word, or a name another connection already shows. Two connections
-- under one name are one Clash proxy / sing-box tag twice, and a client rejects the
-- whole profile over it. Such a name keeps its braces for the operator to rename.

CREATE TEMP TABLE conn_name_strip (kind TEXT NOT NULL, id INTEGER NOT NULL, name TEXT NOT NULL);

-- The same stripping for every source: both variables out, the doubled spaces they
-- leave behind collapsed, the ends trimmed.
INSERT INTO conn_name_strip
SELECT 'inbound', id, trim(replace(replace(replace(replace(replace(name,
       '{flag}', ''), '{country}', ''), '  ', ' '), '  ', ' '), '  ', ' '))
FROM inbounds WHERE instr(name, '{flag}') > 0 OR instr(name, '{country}') > 0;

INSERT INTO conn_name_strip
SELECT 'vless', id, trim(replace(replace(replace(replace(replace(vless_name,
       '{flag}', ''), '{country}', ''), '  ', ' '), '  ', ' '), '  ', ' '))
FROM settings WHERE instr(vless_name, '{flag}') > 0 OR instr(vless_name, '{country}') > 0;

INSERT INTO conn_name_strip
SELECT 'reality', id, trim(replace(replace(replace(replace(replace(reality_name,
       '{flag}', ''), '{country}', ''), '  ', ' '), '  ', ' '), '  ', ' '))
FROM settings WHERE instr(reality_name, '{flag}') > 0 OR instr(reality_name, '{country}') > 0;

INSERT INTO conn_name_strip
SELECT 'hysteria', id, trim(replace(replace(replace(replace(replace(hysteria_name,
       '{flag}', ''), '{country}', ''), '  ', ' '), '  ', ' '), '  ', ' '))
FROM settings WHERE instr(hysteria_name, '{flag}') > 0 OR instr(hysteria_name, '{country}') > 0;

INSERT INTO conn_name_strip
SELECT 'awg', id, trim(replace(replace(replace(replace(replace(awg_name,
       '{flag}', ''), '{country}', ''), '  ', ' '), '  ', ' '), '  ', ' '))
FROM settings WHERE instr(awg_name, '{flag}') > 0 OR instr(awg_name, '{country}') > 0;

-- Custom inbounds first, against the lanes' labels as they stand (a blank lane shows
-- its protocol label). OR IGNORE is the check against other inbounds: a new name
-- another inbound on the same server holds trips idx_inbounds_name, and that one row
-- is skipped instead of failing the upgrade.
UPDATE OR IGNORE inbounds
SET name = (SELECT s.name FROM conn_name_strip s WHERE s.kind = 'inbound' AND s.id = inbounds.id)
WHERE id IN (
    SELECT s.id FROM conn_name_strip s
    WHERE s.kind = 'inbound' AND s.name != '' AND lower(s.name) NOT IN ('auto', 'direct')
      AND NOT EXISTS (
          SELECT 1 FROM settings st WHERE lower(s.name) IN (
              lower(coalesce(nullif(trim(st.vless_name), ''), 'VLESS-TCP-TLS')),
              lower(coalesce(nullif(trim(st.reality_name), ''), 'VLESS-XHTTP-REALITY')),
              lower(coalesce(nullif(trim(st.hysteria_name), ''), 'HYSTERIA-UDP')),
              lower(coalesce(nullif(trim(st.awg_name), ''), 'AMNEZIA-WG'))))
);

-- Then the built-in lanes, one at a time, by the label each would show: the stripped
-- name, or the lane's default label when nothing is left. That label is checked
-- against the inbounds as they now are and the other lanes' labels as they now are.
UPDATE settings SET vless_name = (SELECT s.name FROM conn_name_strip s WHERE s.kind = 'vless')
WHERE EXISTS (
    SELECT 1 FROM conn_name_strip s WHERE s.kind = 'vless'
    AND lower(coalesce(nullif(s.name, ''), 'VLESS-TCP-TLS')) NOT IN ('auto', 'direct',
        lower(coalesce(nullif(trim(settings.reality_name), ''), 'VLESS-XHTTP-REALITY')),
        lower(coalesce(nullif(trim(settings.hysteria_name), ''), 'HYSTERIA-UDP')),
        lower(coalesce(nullif(trim(settings.awg_name), ''), 'AMNEZIA-WG')))
    AND NOT EXISTS (SELECT 1 FROM inbounds i WHERE lower(i.name) = lower(coalesce(nullif(s.name, ''), 'VLESS-TCP-TLS'))));

UPDATE settings SET reality_name = (SELECT s.name FROM conn_name_strip s WHERE s.kind = 'reality')
WHERE EXISTS (
    SELECT 1 FROM conn_name_strip s WHERE s.kind = 'reality'
    AND lower(coalesce(nullif(s.name, ''), 'VLESS-XHTTP-REALITY')) NOT IN ('auto', 'direct',
        lower(coalesce(nullif(trim(settings.vless_name), ''), 'VLESS-TCP-TLS')),
        lower(coalesce(nullif(trim(settings.hysteria_name), ''), 'HYSTERIA-UDP')),
        lower(coalesce(nullif(trim(settings.awg_name), ''), 'AMNEZIA-WG')))
    AND NOT EXISTS (SELECT 1 FROM inbounds i WHERE lower(i.name) = lower(coalesce(nullif(s.name, ''), 'VLESS-XHTTP-REALITY'))));

UPDATE settings SET hysteria_name = (SELECT s.name FROM conn_name_strip s WHERE s.kind = 'hysteria')
WHERE EXISTS (
    SELECT 1 FROM conn_name_strip s WHERE s.kind = 'hysteria'
    AND lower(coalesce(nullif(s.name, ''), 'HYSTERIA-UDP')) NOT IN ('auto', 'direct',
        lower(coalesce(nullif(trim(settings.vless_name), ''), 'VLESS-TCP-TLS')),
        lower(coalesce(nullif(trim(settings.reality_name), ''), 'VLESS-XHTTP-REALITY')),
        lower(coalesce(nullif(trim(settings.awg_name), ''), 'AMNEZIA-WG')))
    AND NOT EXISTS (SELECT 1 FROM inbounds i WHERE lower(i.name) = lower(coalesce(nullif(s.name, ''), 'HYSTERIA-UDP'))));

UPDATE settings SET awg_name = (SELECT s.name FROM conn_name_strip s WHERE s.kind = 'awg')
WHERE EXISTS (
    SELECT 1 FROM conn_name_strip s WHERE s.kind = 'awg'
    AND lower(coalesce(nullif(s.name, ''), 'AMNEZIA-WG')) NOT IN ('auto', 'direct',
        lower(coalesce(nullif(trim(settings.vless_name), ''), 'VLESS-TCP-TLS')),
        lower(coalesce(nullif(trim(settings.reality_name), ''), 'VLESS-XHTTP-REALITY')),
        lower(coalesce(nullif(trim(settings.hysteria_name), ''), 'HYSTERIA-UDP')))
    AND NOT EXISTS (SELECT 1 FROM inbounds i WHERE lower(i.name) = lower(coalesce(nullif(s.name, ''), 'AMNEZIA-WG'))));

DROP TABLE conn_name_strip;
