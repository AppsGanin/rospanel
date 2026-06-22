package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// GetSettings returns the singleton settings row.
func (s *Store) GetSettings() (*model.Settings, error) {
	var st model.Settings
	var updated int64
	var vlessEn, trojanEn, hysteriaEn, setupDone int
	var realityEn, proxyModeEn int
	var subBase64, subEmailInName, subRouting, warpEn int
	var operaEn int
	var tlsFragment, tlsMin13, blockQUIC int
	var tgBotEn int
	var routingCfg string
	err := s.db.QueryRow(`
		SELECT id, host, sni, tls_mode, acme_email, cert_path, key_path,
		       vless_port, config_revision, last_config_error, updated_at,
		       panel_secret_path, decoy_template,
		       ws_path, trojan_port, hysteria_port, hop_start, hop_end,
		       vless_enabled, trojan_enabled, hysteria_enabled,
		       setup_done, timezone,
		       sub_base64, sub_email_in_name, sub_title, sub_routing,
		       sub_routing_happ, sub_routing_incy, sub_routing_mihomo,
		       sub_update_interval, xray_dns,
		       warp_enabled, warp_private_key, warp_public_key, warp_endpoint,
		       warp_address_v4, warp_address_v6, warp_reserved, routing_config,
		       vless_fp, trojan_fp, reality_fp, hop_interval,
		       reality_enabled, reality_port, reality_dest, reality_private_key,
		       reality_public_key, reality_short_id, reality_service_name,
		       proxy_mode_enabled, proxy_mode_type, proxy_mode_port,
		       proxy_mode_user, proxy_mode_pass,
		       tls_fragment, tls_min13, block_quic,
		       reality_max_time_diff, sub_path,
		       acme_provider, zerossl_eab_kid, zerossl_eab_hmac,
		       opera_enabled, opera_country, opera_port,
		       tg_bot_enabled, tg_bot_token, tg_chat_ids, tg_link_code, tg_backup_cron
		FROM settings WHERE id = 1`,
	).Scan(
		&st.ID, &st.Host, &st.SNI, &st.TLSMode, &st.ACMEEmail, &st.CertPath, &st.KeyPath,
		&st.VLESSPort, &st.ConfigRevision, &st.LastConfigError, &updated,
		&st.PanelSecretPath, &st.DecoyTemplate,
		&st.WSPath, &st.TrojanPort, &st.HysteriaPort, &st.HopStart, &st.HopEnd,
		&vlessEn, &trojanEn, &hysteriaEn,
		&setupDone, &st.Timezone,
		&subBase64, &subEmailInName, &st.SubTitle, &subRouting,
		&st.SubRoutingHapp, &st.SubRoutingIncy, &st.SubRoutingMihomo,
		&st.SubUpdateInterval, &st.XrayDNS,
		&warpEn, &st.WarpPrivateKey, &st.WarpPublicKey, &st.WarpEndpoint,
		&st.WarpAddressV4, &st.WarpAddressV6, &st.WarpReserved, &routingCfg,
		&st.VLESSFp, &st.TrojanFp, &st.RealityFp, &st.HopInterval,
		&realityEn, &st.RealityPort, &st.RealityDest, &st.RealityPrivateKey,
		&st.RealityPublicKey, &st.RealityShortID, &st.RealityServiceName,
		&proxyModeEn, &st.ProxyModeType, &st.ProxyModePort,
		&st.ProxyModeUser, &st.ProxyModePass,
		&tlsFragment, &tlsMin13, &blockQUIC,
		&st.RealityMaxTimeDiff, &st.SubPath,
		&st.ACMEProvider, &st.ZeroSSLEABKID, &st.ZeroSSLEABHMAC,
		&operaEn, &st.OperaCountry, &st.OperaPort,
		&tgBotEn, &st.TGBotToken, &st.TGChatIDs, &st.TGLinkCode, &st.TGBackupCron,
	)
	if err != nil {
		return nil, err
	}
	if routingCfg != "" {
		_ = json.Unmarshal([]byte(routingCfg), &st.Routing)
	} else {
		// Never configured: ad-blocking is on by default.
		st.Routing = model.RoutingConfig{BlockAds: true}
	}
	st.UpdatedAt = time.Unix(updated, 0)
	st.VLESSEnabled = vlessEn != 0
	st.TrojanEnabled = trojanEn != 0
	st.HysteriaEnabled = hysteriaEn != 0
	st.RealityEnabled = realityEn != 0
	st.ProxyModeEnabled = proxyModeEn != 0
	st.SetupDone = setupDone != 0
	st.SubBase64 = subBase64 != 0
	st.SubEmailInName = subEmailInName != 0
	st.SubRouting = subRouting != 0
	st.WarpEnabled = warpEn != 0
	st.OperaEnabled = operaEn != 0
	st.TLSFragment = tlsFragment != 0
	st.TLSMin13 = tlsMin13 != 0
	st.BlockQUIC = blockQUIC != 0
	st.TGBotEnabled = tgBotEn != 0
	return &st, nil
}

// SetTelegramBot persists the bot's enable flag, token, and backup schedule (a
// 5-field cron expression in the operator timezone; empty disables scheduling).
func (s *Store) SetTelegramBot(enabled bool, token, cron string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET tg_bot_enabled = ?, tg_bot_token = ?, tg_backup_cron = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		boolToInt(enabled), token, cron,
	)
	return err
}

// SetTelegramLinkCode stores (or clears, with "") the pending one-time linking code.
func (s *Store) SetTelegramLinkCode(code string) error {
	return s.setSetting("tg_link_code", code)
}

// SetTelegramChats replaces the comma-separated set of authorized chat IDs.
func (s *Store) SetTelegramChats(csv string) error {
	return s.setSetting("tg_chat_ids", csv)
}

// SetAntiDPI persists the anti-DPI transport-hardening settings (Settings →
// Подключения): client-config shaping (TLS fragmentation, QUIC block) and the
// server-inbound knobs (TLS 1.3 floor, REALITY anti-replay window + donor port).
func (s *Store) SetAntiDPI(tlsFragment, tlsMin13, blockQUIC bool, realityMaxTimeDiff int) error {
	_, err := s.db.Exec(
		`UPDATE settings SET tls_fragment = ?, tls_min13 = ?, block_quic = ?,
		        reality_max_time_diff = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		tlsFragment, tlsMin13, blockQUIC, realityMaxTimeDiff,
	)
	return err
}

// SetHysteriaPorts persists the Hysteria2 base port, hop range, and hop interval.
func (s *Store) SetHysteriaPorts(port, hopStart, hopEnd int, interval string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET hysteria_port = ?, hop_start = ?, hop_end = ?,
		        hop_interval = ?, updated_at = unixepoch() WHERE id = 1`,
		port, hopStart, hopEnd, interval,
	)
	return err
}

// SetFingerprints persists the per-connection uTLS fingerprints used in links.
func (s *Store) SetFingerprints(vless, trojan, reality string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET vless_fp = ?, trojan_fp = ?, reality_fp = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		vless, trojan, reality,
	)
	return err
}

// SetRoutingConfig persists the structured routing configuration as JSON.
func (s *Store) SetRoutingConfig(cfg model.RoutingConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE settings SET routing_config = ?, updated_at = unixepoch() WHERE id = 1`,
		string(b),
	)
	return err
}

// SetWarp persists the WARP enabled flag plus the provisioned account fields.
func (s *Store) SetWarp(st *model.Settings) error {
	_, err := s.db.Exec(`
		UPDATE settings SET
			warp_enabled = ?, warp_private_key = ?, warp_public_key = ?,
			warp_endpoint = ?, warp_address_v4 = ?, warp_address_v6 = ?,
			warp_reserved = ?, updated_at = unixepoch()
		WHERE id = 1`,
		boolToInt(st.WarpEnabled), st.WarpPrivateKey, st.WarpPublicKey,
		st.WarpEndpoint, st.WarpAddressV4, st.WarpAddressV6, st.WarpReserved,
	)
	return err
}

// SetOpera persists the Opera VPN egress settings (enable flag, region, and the
// local proxy port the opera-proxy helper listens on).
func (s *Store) SetOpera(enabled bool, country string, port int) error {
	_, err := s.db.Exec(
		`UPDATE settings SET opera_enabled = ?, opera_country = ?, opera_port = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		boolToInt(enabled), country, port,
	)
	return err
}

// SetSubSettings persists the subscription delivery settings.
func (s *Store) SetSubSettings(st *model.Settings) error {
	_, err := s.db.Exec(`
		UPDATE settings SET
			sub_path = ?,
			sub_base64 = ?, sub_email_in_name = ?, sub_title = ?, sub_routing = ?,
			sub_routing_happ = ?, sub_routing_incy = ?, sub_routing_mihomo = ?,
			sub_update_interval = ?,
			updated_at = unixepoch()
		WHERE id = 1`,
		st.SubPath,
		st.SubBase64, st.SubEmailInName, st.SubTitle, st.SubRouting,
		st.SubRoutingHapp, st.SubRoutingIncy, st.SubRoutingMihomo,
		st.SubUpdateInterval,
	)
	return err
}

// SetTimezone persists the operator's IANA timezone (e.g. "Europe/Moscow").
func (s *Store) SetTimezone(tz string) error { return s.setSetting("timezone", tz) }

// SetSetupDone marks the first-run wizard as completed.
func (s *Store) SetSetupDone(done bool) error { return s.setSetting("setup_done", done) }

// MustChangePassword reports whether the admin must replace the default password
// before the rest of the panel unlocks (set at first-run bootstrap, cleared on the
// first password change).
func (s *Store) MustChangePassword() (bool, error) {
	var v int
	err := s.db.QueryRow(`SELECT must_change_password FROM settings WHERE id = 1`).Scan(&v)
	return v != 0, err
}

// SetMustChangePassword sets or clears the forced-password-change gate.
func (s *Store) SetMustChangePassword(must bool) error {
	return s.setSetting("must_change_password", must)
}

// protocolColumn maps a public protocol name to its settings toggle column.
var protocolColumn = map[string]string{
	"vless":     "vless_enabled",
	"trojan":    "trojan_enabled",
	"hysteria2": "hysteria_enabled",
	"reality":   "reality_enabled",
}

// SetProtocolEnabled flips a single protocol's on/off toggle.
func (s *Store) SetProtocolEnabled(name string, enabled bool) error {
	col, ok := protocolColumn[name]
	if !ok {
		return fmt.Errorf("unknown protocol %q", name)
	}
	return s.setSetting(col, enabled)
}

// setSetting writes one settings column and bumps updated_at. col is always a
// hardcoded literal or allow-listed value, so the concatenation is injection-safe.
func (s *Store) setSetting(col string, val any) error {
	_, err := s.db.Exec(
		`UPDATE settings SET `+col+` = ?, updated_at = unixepoch() WHERE id = 1`, val)
	return err
}

// SetTLS persists host/SNI/cert configuration (used on first boot).
func (s *Store) SetTLS(host, sni, mode, certPath, keyPath string) error {
	_, err := s.db.Exec(`
		UPDATE settings
		SET host = ?, sni = ?, tls_mode = ?, cert_path = ?, key_path = ?,
		    updated_at = unixepoch()
		WHERE id = 1`,
		host, sni, mode, certPath, keyPath,
	)
	return err
}

// SetSecretPath persists the hidden panel path segment.
func (s *Store) SetSecretPath(p string) error { return s.setSetting("panel_secret_path", p) }

// SetACMEProvider persists the ACME CA selection and (for ZeroSSL) the External
// Account Binding credentials. An empty provider defaults to "letsencrypt".
func (s *Store) SetACMEProvider(provider, eabKID, eabHMAC string) error {
	if provider == "" {
		provider = "letsencrypt"
	}
	_, err := s.db.Exec(
		`UPDATE settings SET acme_provider = ?, zerossl_eab_kid = ?,
		        zerossl_eab_hmac = ?, updated_at = unixepoch() WHERE id = 1`,
		provider, eabKID, eabHMAC,
	)
	return err
}

// SetTLSMode persists the TLS mode, domain (host), SNI and ACME e-mail.
func (s *Store) SetTLSMode(mode, host, sni, acmeEmail string) error {
	_, err := s.db.Exec(`
		UPDATE settings
		SET tls_mode = ?, host = ?, sni = ?, acme_email = ?, updated_at = unixepoch()
		WHERE id = 1`,
		mode, host, sni, acmeEmail,
	)
	return err
}

// SetXrayDNS persists the operator's Xray DNS servers.
func (s *Store) SetXrayDNS(dns string) error { return s.setSetting("xray_dns", dns) }

// SetDecoyTemplate persists the masquerade (decoy) template slug.
func (s *Store) SetDecoyTemplate(name string) error { return s.setSetting("decoy_template", name) }

// SetProxyMode persists the forward-proxy inbound configuration.
func (s *Store) SetProxyMode(enabled bool, typ string, port int, user, pass string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET proxy_mode_enabled = ?, proxy_mode_type = ?,
		        proxy_mode_port = ?, proxy_mode_user = ?, proxy_mode_pass = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		enabled, typ, port, user, pass,
	)
	return err
}

// SetWSPath persists the Trojan-WS path.
func (s *Store) SetWSPath(p string) error { return s.setSetting("ws_path", p) }

// SetRealityPorts persists the REALITY port and destination (SNI/serverName).
func (s *Store) SetRealityPorts(port int, dest string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET reality_port = ?, reality_dest = ?,
		        updated_at = unixepoch() WHERE id = 1`, port, dest,
	)
	return err
}

// SetRealityKeys persists a freshly generated REALITY keypair, shortId, and gRPC
// service name.
func (s *Store) SetRealityKeys(priv, pub, shortID, serviceName string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET reality_private_key = ?, reality_public_key = ?,
		        reality_short_id = ?, reality_service_name = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		priv, pub, shortID, serviceName,
	)
	return err
}

// MarkConfigApplied bumps the config revision and clears any prior error.
func (s *Store) MarkConfigApplied() error {
	_, err := s.db.Exec(`
		UPDATE settings
		SET config_revision = config_revision + 1, last_config_error = '',
		    updated_at = unixepoch()
		WHERE id = 1`)
	return err
}

// SetConfigError records the last failed config-apply error.
func (s *Store) SetConfigError(msg string) error { return s.setSetting("last_config_error", msg) }
