package store

import (
	"database/sql"
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
	var subBase64, subNameInTitle, subRouting, warpEn int
	var operaEn int
	var tlsFragment, tlsMin13, blockQUIC int
	var tgBotEn, tgUserBotEn, tgUserRegEn, billingEn int
	var yooEn, cryptoEn, yooTest, cryptoTest int
	var routingCfg string
	err := s.db.QueryRow(`
		SELECT id, host, sni, tls_mode, acme_email, cert_path, key_path,
		       vless_port, config_revision, last_config_error, updated_at,
		       panel_secret_path, panel_name, panel_theme, decoy_template,
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
		       tg_bot_enabled, tg_bot_token, tg_chat_ids, tg_link_code, tg_backup_cron,
		       tg_user_bot_enabled, tg_user_bot_token, tg_user_reg_enabled,
		       billing_enabled, billing_trial_days, billing_free_plan_id,
		       billing_trial_plan_id, billing_payment_note,
		       yookassa_enabled, yookassa_shop_id, yookassa_secret_key, yookassa_test,
		       cryptobot_enabled, cryptobot_token, cryptobot_testnet, payment_webhook_secret,
		       tg_admin_events, api_path,
		       vless_name, reality_name, trojan_name, hysteria_name,
		       local_backup_cron, local_backup_keep
		FROM settings WHERE id = 1`,
	).Scan(
		&st.ID, &st.Host, &st.SNI, &st.TLSMode, &st.ACMEEmail, &st.CertPath, &st.KeyPath,
		&st.VLESSPort, &st.ConfigRevision, &st.LastConfigError, &updated,
		&st.PanelSecretPath, &st.PanelName, &st.PanelTheme, &st.DecoyTemplate,
		&st.WSPath, &st.TrojanPort, &st.HysteriaPort, &st.HopStart, &st.HopEnd,
		&vlessEn, &trojanEn, &hysteriaEn,
		&setupDone, &st.Timezone,
		&subBase64, &subNameInTitle, &st.SubTitle, &subRouting,
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
		&tgUserBotEn, &st.TGUserBotToken, &tgUserRegEn,
		&billingEn, &st.BillingTrialDays, &st.BillingFreePlanID,
		&st.BillingTrialPlanID, &st.BillingPaymentNote,
		&yooEn, &st.YooKassaShopID, &st.YooKassaSecretKey, &yooTest,
		&cryptoEn, &st.CryptoBotToken, &cryptoTest, &st.PaymentWebhookSecret,
		&st.TGAdminEvents, &st.APIPath,
		&st.VLESSName, &st.RealityName, &st.TrojanName, &st.HysteriaName,
		&st.LocalBackupCron, &st.LocalBackupKeep,
	)
	if err != nil {
		return nil, err
	}
	if routingCfg != "" {
		_ = json.Unmarshal([]byte(routingCfg), &st.Routing)
		// A config saved before egress lanes existed carries a single proxy pool in
		// the deprecated Proxy* fields; fold it into a lane so the rest of the code
		// only ever sees the lane model.
		st.Routing.MigrateLanes()
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
	st.SubNameInTitle = subNameInTitle != 0
	st.SubRouting = subRouting != 0
	st.WarpEnabled = warpEn != 0
	st.OperaEnabled = operaEn != 0
	st.TLSFragment = tlsFragment != 0
	st.TLSMin13 = tlsMin13 != 0
	st.BlockQUIC = blockQUIC != 0
	st.TGBotEnabled = tgBotEn != 0
	st.TGUserBotEnabled = tgUserBotEn != 0
	st.TGUserRegEnabled = tgUserRegEn != 0
	st.BillingEnabled = billingEn != 0
	st.YooKassaEnabled = yooEn != 0
	st.YooKassaTest = yooTest != 0
	st.CryptoBotEnabled = cryptoEn != 0
	st.CryptoBotTestnet = cryptoTest != 0
	// Decrypt at-rest secret fields (legacy plaintext rows pass through).
	st.TGBotToken = decField(st.TGBotToken)
	st.TGUserBotToken = decField(st.TGUserBotToken)
	st.WarpPrivateKey = decField(st.WarpPrivateKey)
	st.RealityPrivateKey = decField(st.RealityPrivateKey)
	st.ProxyModePass = decField(st.ProxyModePass)
	st.ZeroSSLEABHMAC = decField(st.ZeroSSLEABHMAC)
	st.YooKassaSecretKey = decField(st.YooKassaSecretKey)
	st.CryptoBotToken = decField(st.CryptoBotToken)
	return &st, nil
}

// SetTelegramBot persists the bot's enable flag, token, and backup schedule (a
// 5-field cron expression in the operator timezone; empty disables scheduling).
func (s *Store) SetTelegramBot(enabled bool, token, cron string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET tg_bot_enabled = ?, tg_bot_token = ?, tg_backup_cron = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		boolToInt(enabled), encField(token), cron,
	)
	return err
}

// SetLocalBackup persists the local backup schedule (a 5-field cron expression in
// the operator timezone; empty disables it) and how many archives to retain.
func (s *Store) SetLocalBackup(cron string, keep int) error {
	_, err := s.db.Exec(
		`UPDATE settings SET local_backup_cron = ?, local_backup_keep = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		cron, keep,
	)
	return err
}

// SetTelegramUserBot persists the public user bot's enable flag, token, and the
// self-registration toggle.
func (s *Store) SetTelegramUserBot(enabled bool, token string, regEnabled bool) error {
	_, err := s.db.Exec(
		`UPDATE settings SET tg_user_bot_enabled = ?, tg_user_bot_token = ?,
		        tg_user_reg_enabled = ?, updated_at = unixepoch() WHERE id = 1`,
		boolToInt(enabled), encField(token), boolToInt(regEnabled),
	)
	return err
}

// SetAdminEvents persists the admin notification bitmask (model.AdminEvent* flags).
func (s *Store) SetAdminEvents(mask int64) error {
	_, err := s.db.Exec(
		`UPDATE settings SET tg_admin_events = ?, updated_at = unixepoch() WHERE id = 1`,
		mask,
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

// SetProtocolNames persists the custom per-connection display names (empty ⇒ the
// default protocol label is used at render time).
func (s *Store) SetProtocolNames(vless, reality, trojan, hysteria string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET vless_name = ?, reality_name = ?, trojan_name = ?,
		        hysteria_name = ?, updated_at = unixepoch() WHERE id = 1`,
		vless, reality, trojan, hysteria,
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
		boolToInt(st.WarpEnabled), encField(st.WarpPrivateKey), st.WarpPublicKey,
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
		st.SubBase64, st.SubNameInTitle, st.SubTitle, st.SubRouting,
		st.SubRoutingHapp, st.SubRoutingIncy, st.SubRoutingMihomo,
		st.SubUpdateInterval,
	)
	return err
}

// SetTimezone persists the operator's IANA timezone (e.g. "Europe/Moscow").
func (s *Store) SetTimezone(tz string) error { return s.setSetting("timezone", tz) }

// SetSetupDone marks the first-run wizard as completed.
func (s *Store) SetSetupDone(done bool) error { return s.setSetting("setup_done", done) }

// The forced-password-change gate used to live here, on the settings singleton.
// It now lives per-admin (admins.must_change_password) — with several admins,
// "this panel is gated" and "this admin is gated" are different questions, and only
// the second one is answerable. Migration 0023 moved the value across; the settings
// column is still there but nothing reads or writes it. See store/admins.go.

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

// SetAPIPath persists the external-API URL segment (empty disables the surface).
func (s *Store) SetAPIPath(p string) error { return s.setSetting("api_path", p) }

// SetACMEProvider persists the ACME CA selection and (for ZeroSSL) the External
// Account Binding credentials. An empty provider defaults to "letsencrypt".
func (s *Store) SetACMEProvider(provider, eabKID, eabHMAC string) error {
	if provider == "" {
		provider = "letsencrypt"
	}
	_, err := s.db.Exec(
		`UPDATE settings SET acme_provider = ?, zerossl_eab_kid = ?,
		        zerossl_eab_hmac = ?, updated_at = unixepoch() WHERE id = 1`,
		provider, eabKID, encField(eabHMAC),
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

func (s *Store) SetPanelName(name string) error { return s.setSetting("panel_name", name) }

func (s *Store) SetPanelTheme(themeJSON string) error { return s.setSetting("panel_theme", themeJSON) }

// SetProxyMode persists the forward-proxy inbound configuration.
func (s *Store) SetProxyMode(enabled bool, typ string, port int, user, pass string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET proxy_mode_enabled = ?, proxy_mode_type = ?,
		        proxy_mode_port = ?, proxy_mode_user = ?, proxy_mode_pass = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		enabled, typ, port, user, encField(pass),
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
		encField(priv), pub, shortID, serviceName,
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

// PeekTimezone reads the operator's configured IANA timezone straight from the DB
// file, without opening the full store (no migrations, no encryption key needed —
// the zone isn't a secret). It exists so main() can stamp log lines in the
// operator's zone from the very FIRST line: the real store isn't open that early,
// and setting the zone later would leave the opening boot lines in the server's
// system zone while everything after them used the operator's — timestamps jumping
// an hour mid-boot.
//
// Returns "" for a fresh install (no DB / no row yet), which the caller reads as
// "use server-local".
func PeekTimezone(dbPath string) string {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return ""
	}
	defer db.Close()
	var tz string
	if err := db.QueryRow(`SELECT timezone FROM settings WHERE id = 1`).Scan(&tz); err != nil {
		return ""
	}
	return tz
}
