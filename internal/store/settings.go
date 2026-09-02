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
	var vlessEn, hysteriaEn, setupDone int
	var realityEn, proxySocksEn, proxyHTTPEn int
	var proxyAccounts string
	var subBase64, subNameInTitle, subRouting, warpEn int
	var operaEn int
	var tlsFragment, tlsMin13, blockQUIC int
	var tgBotEn, tgUserBotEn, tgUserRegEn, billingEn int
	var tgSupportEn int
	var abuseEn int
	var hwidEn, hwidRequire int
	var subShowConfigs, statusEn, maintenanceMode, probeDetect, watchdogEnabled int
	var probeBlock int
	var routingCfg, subRulesJSON, subDPIJSON string
	var masterHideFull, awgEn, hideOffline int
	var awgParamsJSON, connPolicyJSON string
	err := s.db.QueryRow(`
		SELECT id, host, sni, tls_mode, acme_email, cert_path, key_path,
		       vless_port, config_revision, last_config_error, updated_at,
		       panel_secret_path, panel_name, panel_theme, decoy_template,
		       hysteria_port, hop_start, hop_end,
		       vless_enabled, hysteria_enabled,
		       setup_done, timezone,
		       sub_base64, sub_email_in_name, sub_title, sub_routing,
		       sub_routing_happ, sub_routing_incy, sub_routing_mihomo,
		       sub_update_interval, xray_dns,
		       warp_enabled, warp_private_key, warp_public_key, warp_endpoint,
		       warp_address_v4, warp_address_v6, warp_reserved, routing_config,
		       vless_fp, reality_fp, hop_interval,
		       reality_enabled, reality_port, reality_dest, reality_private_key,
		       reality_public_key, reality_short_id, reality_path,
		       proxy_socks_enabled, proxy_socks_port,
		       proxy_http_enabled, proxy_http_port, proxy_accounts,
		       tls_fragment, tls_min13, block_quic,
		       reality_max_time_diff, sub_path,
		       acme_provider, zerossl_eab_kid, zerossl_eab_hmac,
		       opera_enabled, opera_country, opera_port,
		       tg_bot_enabled, tg_bot_token, tg_chat_ids, tg_link_code, tg_backup_cron,
		       tg_user_bot_enabled, tg_user_bot_token, tg_user_reg_enabled,
		       tg_user_reg_mode, tg_user_reg_code,
		       billing_enabled, billing_free_plan_id,
		       billing_trial_plan_id, billing_payment_note,
		       payment_webhook_secret,
		       tg_admin_events, api_path,
		       vless_name, reality_name, hysteria_name,
		       local_backup_cron, local_backup_keep,
		       sub_announce, user_autodelete_days, node_api_path, master_label,
		       geo_refresh_hours, iplist_refresh_hours,
		       tg_support_enabled, tg_support_bot_token, tg_support_bot_username,
		       tg_support_group_id, tg_support_greeting, tg_lang, tg_proxy, tg_proxy_mode,
		       tg_user_events, tg_user_expiring_days,
		       abuse_enabled, abuse_categories, abuse_custom, abuse_alert_min,
		       abuse_warn_min, abuse_throttle_min, abuse_throttle_kbps, abuse_disable_min, abuse_hours,
		       hwid_enabled, hwid_require, hwid_fallback_limit, hwid_ttl_days,
		       device_count_mode,
		       sub_show_configs, status_enabled, status_path, sub_rules, maintenance_mode,
		       probe_detect, watchdog_enabled, probe_block, sub_dpi,
		       sub_order_mode, master_country, master_sort_weight, master_capacity, master_hide_when_full,
		       sub_hide_offline, conn_policy,
		       awg_enabled, awg_port, awg_private_key, awg_public_key, awg_params, awg_name, awg_dns
		FROM settings WHERE id = 1`,
	).Scan(
		&st.ID, &st.Host, &st.SNI, &st.TLSMode, &st.ACMEEmail, &st.CertPath, &st.KeyPath,
		&st.VLESSPort, &st.ConfigRevision, &st.LastConfigError, &updated,
		&st.PanelSecretPath, &st.PanelName, &st.PanelTheme, &st.DecoyTemplate,
		&st.HysteriaPort, &st.HopStart, &st.HopEnd,
		&vlessEn, &hysteriaEn,
		&setupDone, &st.Timezone,
		&subBase64, &subNameInTitle, &st.SubTitle, &subRouting,
		&st.SubRoutingHapp, &st.SubRoutingIncy, &st.SubRoutingMihomo,
		&st.SubUpdateInterval, &st.XrayDNS,
		&warpEn, &st.WarpPrivateKey, &st.WarpPublicKey, &st.WarpEndpoint,
		&st.WarpAddressV4, &st.WarpAddressV6, &st.WarpReserved, &routingCfg,
		&st.VLESSFp, &st.RealityFp, &st.HopInterval,
		&realityEn, &st.RealityPort, &st.RealityDest, &st.RealityPrivateKey,
		&st.RealityPublicKey, &st.RealityShortID, &st.RealityPath,
		&proxySocksEn, &st.ProxySocksPort,
		&proxyHTTPEn, &st.ProxyHTTPPort, &proxyAccounts,
		&tlsFragment, &tlsMin13, &blockQUIC,
		&st.RealityMaxTimeDiff, &st.SubPath,
		&st.ACMEProvider, &st.ZeroSSLEABKID, &st.ZeroSSLEABHMAC,
		&operaEn, &st.OperaCountry, &st.OperaPort,
		&tgBotEn, &st.TGBotToken, &st.TGChatIDs, &st.TGLinkCode, &st.TGBackupCron,
		&tgUserBotEn, &st.TGUserBotToken, &tgUserRegEn,
		&st.TGUserRegMode, &st.TGUserRegCode,
		&billingEn, &st.BillingFreePlanID,
		&st.BillingTrialPlanID, &st.BillingPaymentNote,
		&st.PaymentWebhookSecret,
		&st.TGAdminEvents, &st.APIPath,
		&st.VLESSName, &st.RealityName, &st.HysteriaName,
		&st.LocalBackupCron, &st.LocalBackupKeep,
		&st.SubAnnounce, &st.UserAutoDeleteDays, &st.NodeAPIPath, &st.MasterLabel,
		&st.GeoRefreshHours, &st.IPListRefreshHours,
		&tgSupportEn, &st.TGSupportBotToken, &st.TGSupportBotUsername,
		&st.TGSupportGroupID, &st.TGSupportGreeting, &st.TGLang, &st.TGProxy, &st.TGProxyMode,
		&st.TGUserEvents, &st.TGUserExpiringDays,
		&abuseEn, &st.AbuseCategories, &st.AbuseCustom, &st.AbuseAlertMin,
		&st.AbuseMeasures.WarnMin, &st.AbuseMeasures.ThrottleMin, &st.AbuseMeasures.ThrottleKbps,
		&st.AbuseMeasures.DisableMin, &st.AbuseMeasures.Hours,
		&hwidEn, &hwidRequire, &st.HWIDFallbackLimit, &st.HWIDTTLDays,
		&st.DeviceCountMode,
		&subShowConfigs, &statusEn, &st.StatusPath, &subRulesJSON, &maintenanceMode,
		&probeDetect, &watchdogEnabled, &probeBlock, &subDPIJSON,
		&st.SubOrderMode, &st.MasterPlacement.Country, &st.MasterPlacement.Weight,
		&st.MasterPlacement.Capacity, &masterHideFull, &hideOffline, &connPolicyJSON,
		&awgEn, &st.AWGPort, &st.AWGPrivateKey, &st.AWGPublicKey, &awgParamsJSON, &st.AWGName, &st.AWGDNS,
	)
	if err != nil {
		return nil, err
	}
	if subRulesJSON != "" {
		_ = json.Unmarshal([]byte(subRulesJSON), &st.SubRules)
	}
	// A blank column (pre-0059, or never saved) reads as the defaults with every
	// switch off; a corrupt one too — the subscription must keep serving.
	st.MasterPlacement.HideWhenFull = masterHideFull != 0
	st.SubHideOffline = hideOffline != 0
	// A blank column (pre-0063, or never saved) reads as the feature off; so does a
	// corrupt one — a policy nobody can parse must not start refusing connections.
	st.ConnPolicy = model.DefaultConnPolicy()
	if connPolicyJSON != "" {
		var p model.ConnPolicy
		if json.Unmarshal([]byte(connPolicyJSON), &p) == nil {
			st.ConnPolicy = p.Normalized()
		}
	}
	st.AWGEnabled = awgEn != 0
	st.AWGPrivateKey = decField(st.AWGPrivateKey)
	if awgParamsJSON != "" {
		_ = json.Unmarshal([]byte(awgParamsJSON), &st.AWGParams)
	}
	st.SubOrderMode = model.OrderModeOr(st.SubOrderMode)
	st.SubDPI = model.DefaultSubDPI()
	if subDPIJSON != "" {
		if err := json.Unmarshal([]byte(subDPIJSON), &st.SubDPI); err != nil {
			st.SubDPI = model.DefaultSubDPI()
		}
		st.SubDPI = st.SubDPI.Normalized()
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
	st.HysteriaEnabled = hysteriaEn != 0
	st.RealityEnabled = realityEn != 0
	st.ProxySocksEnabled = proxySocksEn != 0
	st.ProxyHTTPEnabled = proxyHTTPEn != 0
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
	st.TGSupportEnabled = tgSupportEn != 0
	st.BillingEnabled = billingEn != 0
	st.AbuseEnabled = abuseEn != 0
	st.HWIDEnabled = hwidEn != 0
	st.HWIDRequire = hwidRequire != 0
	st.SubShowConfigs = subShowConfigs != 0
	st.StatusEnabled = statusEn != 0
	st.MaintenanceMode = maintenanceMode != 0
	st.ProbeDetect = probeDetect != 0
	st.WatchdogEnabled = watchdogEnabled != 0
	st.ProbeBlock = probeBlock != 0
	// Decrypt at-rest secret fields (legacy plaintext rows pass through).
	st.TGBotToken = decField(st.TGBotToken)
	st.TGUserBotToken = decField(st.TGUserBotToken)
	st.TGSupportBotToken = decField(st.TGSupportBotToken)
	// The proxy URL is a secret like the tokens above it: it commonly carries
	// user:pass credentials for the operator's proxy.
	st.TGProxy = decField(st.TGProxy)
	st.WarpPrivateKey = decField(st.WarpPrivateKey)
	st.RealityPrivateKey = decField(st.RealityPrivateKey)
	st.ProxyAccounts = decodeProxyAccounts(proxyAccounts)
	st.ZeroSSLEABHMAC = decField(st.ZeroSSLEABHMAC)
	return &st, nil
}

// SetTelegramBot persists the bot's enable flag, token, and backup schedule (a
// 5-field cron expression in the operator timezone; empty disables scheduling).
// lang is the language the admin bot writes in; empty leaves the panel default.
func (s *Store) SetTelegramBot(enabled bool, token, cron, lang string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET tg_bot_enabled = ?, tg_bot_token = ?, tg_backup_cron = ?,
		        tg_lang = ?, updated_at = unixepoch() WHERE id = 1`,
		boolToInt(enabled), encField(token), cron, lang,
	)
	return err
}

// SetTelegramProxy persists how Telegram is reached: the mode (direct/warp/opera/
// custom) and, for the custom mode, the URL. It gets its own setter rather than
// riding along in SetTelegramBot because it is not the admin bot's: the user bot,
// the support bot and the Mini App SDK fetch all read the same value.
//
// The URL is written whatever the mode, so switching to warp/opera and back leaves
// the operator's hand-typed address still in the box instead of erasing it.
//
// Encrypted at rest — a proxy URL usually carries credentials. The mode is not: it
// is one of four fixed words and encrypting it would only make the column
// unreadable in a support session.
func (s *Store) SetTelegramProxy(mode, raw string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET tg_proxy_mode = ?, tg_proxy = ?, updated_at = unixepoch()
		 WHERE id = 1`,
		mode, encField(raw),
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
// self-registration mode + invite code. tg_user_reg_enabled is kept as a derived
// mirror (mode != off) for any legacy reader.
func (s *Store) SetTelegramUserBot(enabled bool, token, regMode, regCode string) error {
	regOpen := regMode != model.RegOff
	_, err := s.db.Exec(
		`UPDATE settings SET tg_user_bot_enabled = ?, tg_user_bot_token = ?,
		        tg_user_reg_enabled = ?, tg_user_reg_mode = ?, tg_user_reg_code = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		boolToInt(enabled), encField(token), boolToInt(regOpen), regMode, regCode,
	)
	return err
}

// SetTelegramSupport persists the support relay configuration: the enable flag, the
// dedicated support bot's token, its resolved @username (cached so the user bot can
// render a t.me link without a getMe on every menu draw), the forum supergroup id
// admins answer in, and the greeting shown on /start.
func (s *Store) SetTelegramSupport(enabled bool, token, username string, groupID int64, greeting string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET tg_support_enabled = ?, tg_support_bot_token = ?,
		        tg_support_bot_username = ?, tg_support_group_id = ?,
		        tg_support_greeting = ?, updated_at = unixepoch() WHERE id = 1`,
		boolToInt(enabled), encField(token), username, groupID, greeting,
	)
	return err
}

// SetUserEvents persists the user-facing notification bitmask (model.UserEvent*
// flags) and how many days ahead the expiry warning goes out.
func (s *Store) SetUserEvents(mask int64, expiringDays int) error {
	_, err := s.db.Exec(
		`UPDATE settings SET tg_user_events = ?, tg_user_expiring_days = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		mask, expiringDays)
	return err
}

// SetAbuseConfig persists the abuse/blocklist config: master switch, the active
// category bitmask (model.AbuseCat* flags), the operator's custom list, and the
// daily alert threshold.
func (s *Store) SetAbuseConfig(enabled bool, categories int64, custom string, alertMin int) error {
	_, err := s.db.Exec(
		`UPDATE settings SET abuse_enabled = ?, abuse_categories = ?, abuse_custom = ?,
		        abuse_alert_min = ?, updated_at = unixepoch() WHERE id = 1`,
		enabled, categories, custom, alertMin,
	)
	return err
}

// SetAbuseMeasures persists the automatic-response ladder (model.AbuseMeasures).
func (s *Store) SetAbuseMeasures(a model.AbuseMeasures) error {
	_, err := s.db.Exec(
		`UPDATE settings SET abuse_warn_min = ?, abuse_throttle_min = ?, abuse_throttle_kbps = ?,
		        abuse_disable_min = ?, abuse_hours = ?, updated_at = unixepoch() WHERE id = 1`,
		a.WarnMin, a.ThrottleMin, a.ThrottleKbps, a.DisableMin, a.Hours,
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
// Connections): client-config shaping (TLS fragmentation, QUIC block) and the
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
func (s *Store) SetFingerprints(vless, reality string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET vless_fp = ?, reality_fp = ?,
		        updated_at = unixepoch() WHERE id = 1`,
		vless, reality,
	)
	return err
}

// SetProtocolNames persists the custom per-connection display names (empty ⇒ the
// default protocol label is used at render time).
func (s *Store) SetProtocolNames(vless, reality, hysteria string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET vless_name = ?, reality_name = ?,
		        hysteria_name = ?, updated_at = unixepoch() WHERE id = 1`,
		vless, reality, hysteria,
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
			sub_update_interval = ?, sub_announce = ?, sub_show_configs = ?,
			sub_order_mode = ?, sub_hide_offline = ?,
			updated_at = unixepoch()
		WHERE id = 1`,
		st.SubPath,
		st.SubBase64, st.SubNameInTitle, st.SubTitle, st.SubRouting,
		st.SubRoutingHapp, st.SubRoutingIncy, st.SubRoutingMihomo,
		st.SubUpdateInterval, st.SubAnnounce, boolToInt(st.SubShowConfigs),
		model.OrderModeOr(st.SubOrderMode), boolToInt(st.SubHideOffline),
	)
	return err
}

// SetMasterPlacement persists the master's placement (see migration 0060).
func (s *Store) SetMasterPlacement(p model.Placement) error {
	p = p.Normalized()
	_, err := s.db.Exec(`UPDATE settings SET master_country = ?, master_sort_weight = ?, master_capacity = ?,
		master_hide_when_full = ?, updated_at = unixepoch() WHERE id = 1`,
		p.Country, p.Weight, p.Capacity, boolToInt(p.HideWhenFull))
	return err
}

// SetSubRules persists the subscription response rules as a JSON blob. Its own method
// (not folded into SetSubSettings) because the rule editor is its own surface and
// saving a rename shouldn't rewrite the rules, nor the reverse.
func (s *Store) SetSubRules(rules []model.SubRule) error {
	blob := ""
	if len(rules) > 0 {
		b, err := json.Marshal(rules)
		if err != nil {
			return err
		}
		blob = string(b)
	}
	_, err := s.db.Exec(
		`UPDATE settings SET sub_rules = ?, updated_at = unixepoch() WHERE id = 1`, blob)
	return err
}

// SetMaintenanceMode toggles the public-surface maintenance page.
func (s *Store) SetMaintenanceMode(on bool) error {
	return s.setSetting("maintenance_mode", on)
}

// SetProbeDetect toggles secret-path probe detection.
func (s *Store) SetProbeDetect(on bool) error {
	return s.setSetting("probe_detect", on)
}

// SetWatchdogEnabled toggles the wedged-process auto-recovery.
func (s *Store) SetWatchdogEnabled(on bool) error {
	return s.setSetting("watchdog_enabled", on)
}

// SetProbeBlock toggles firewall auto-blocking of flagged scanner IPs.
func (s *Store) SetProbeBlock(on bool) error { return s.setSetting("probe_block", on) }

// SetDeviceCountMode picks which counter enforces a user's device limit.
func (s *Store) SetDeviceCountMode(mode string) error {
	return s.setSetting("device_count_mode", mode)
}

// SetHWIDSettings persists the device-binding settings (Settings → Subscriptions).
func (s *Store) SetHWIDSettings(st *model.Settings) error {
	_, err := s.db.Exec(`
		UPDATE settings SET
			hwid_enabled = ?, hwid_require = ?,
			hwid_fallback_limit = ?, hwid_ttl_days = ?,
			updated_at = unixepoch()
		WHERE id = 1`,
		boolToInt(st.HWIDEnabled), boolToInt(st.HWIDRequire),
		st.HWIDFallbackLimit, st.HWIDTTLDays,
	)
	return err
}

// SetStatusPage persists the public status page's on/off flag and URL segment.
func (s *Store) SetStatusPage(enabled bool, path string) error {
	_, err := s.db.Exec(
		`UPDATE settings SET status_enabled = ?, status_path = ?, updated_at = unixepoch()
		 WHERE id = 1`,
		boolToInt(enabled), path,
	)
	return err
}

// SetUserAutoDeleteDays persists the grace period (in days, past a user's expiry
// date) after which the retention sweep deletes them. 0 disables deletion.
func (s *Store) SetUserAutoDeleteDays(days int) error {
	return s.setSetting("user_autodelete_days", days)
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
	"hysteria2": "hysteria_enabled",
	"reality":   "reality_enabled",
	"awg":       "awg_enabled",
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

// SetNodeAPIPath persists the node sync API URL segment (generated on first node).
func (s *Store) SetNodeAPIPath(p string) error { return s.setSetting("node_api_path", p) }

// SetMasterLabel persists the panel server's display name used in config labels.
func (s *Store) SetMasterLabel(label string) error { return s.setSetting("master_label", label) }

// SetGeoRefresh persists the geo auto-refresh cadence in hours (0 ⇒ never).
func (s *Store) SetGeoRefresh(hours int) error { return s.setSetting("geo_refresh_hours", hours) }

// SetIPListRefresh persists the iplist auto-refresh cadence (hours; 0 ⇒ never).
func (s *Store) SetIPListRefresh(hours int) error {
	return s.setSetting("iplist_refresh_hours", hours)
}

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
		        reality_short_id = ?, reality_path = ?,
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

// SetSubDPI persists the client-side DPI settings as one JSON blob (see
// migration 0059). Callers validate first (model.SubDPI.Validate).
func (s *Store) SetSubDPI(d model.SubDPI) error {
	b, err := json.Marshal(d.Normalized())
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE settings SET sub_dpi = ?, updated_at = unixepoch() WHERE id = 1`, string(b))
	return err
}

// SetAWGConfig persists the master's AmneziaWG port, in-tunnel DNS and display
// name (the toggle goes through SetProtocolEnabled("awg")).
func (s *Store) SetAWGConfig(port int, dns, name string) error {
	_, err := s.db.Exec(`UPDATE settings SET awg_port = ?, awg_dns = ?, awg_name = ?,
		updated_at = unixepoch() WHERE id = 1`, port, dns, name)
	return err
}

// SaveAWGKeys stores the master's AmneziaWG keypair and obfuscation parameters
// (the private key encrypted at rest).
func (s *Store) SaveAWGKeys(priv, pub string, params model.AWGParams) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE settings SET awg_private_key = ?, awg_public_key = ?, awg_params = ?,
		updated_at = unixepoch() WHERE id = 1`, encField(priv), pub, string(b))
	return err
}
