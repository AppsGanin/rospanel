package core

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/netguard"
	"github.com/AppsGanin/rospanel/internal/warp"
)

// SetTimezone validates and persists the operator's IANA timezone, then updates
// the cached location so per-day stats re-bucket immediately.
func (m *Manager) SetTimezone(name string) error {
	name = strings.TrimSpace(name)
	if name != "" {
		if _, err := time.LoadLocation(name); err != nil {
			return invalid("неизвестный часовой пояс %q", name)
		}
	}
	if err := m.store.SetTimezone(name); err != nil {
		return err
	}
	m.tzMu.Lock()
	m.tz = loadLocation(name)
	m.tzMu.Unlock()
	return nil
}

// ChangeAdminPassword hashes and stores a new password for the given admin and
// lifts the forced-password-change gate (a successful change is exactly what the
// gate is waiting for).
func (m *Manager) ChangeAdminPassword(adminID int64, newPassword string) error {
	if len(newPassword) < 8 {
		return invalid("пароль должен быть не короче 8 символов")
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := m.store.UpdateAdminPassword(adminID, hash); err != nil {
		return err
	}
	return m.store.SetMustChangePassword(false)
}

// MustChangePassword reports whether the panel is gated on a forced default-password
// change. A store error is treated as "not gated" so a transient read failure can't
// lock the operator out of their own panel.
func (m *Manager) MustChangePassword() bool {
	must, err := m.store.MustChangePassword()
	return err == nil && must
}

// FinishSetup marks the first-run wizard as completed.
func (m *Manager) FinishSetup() error {
	return m.store.SetSetupDone(true)
}

// UpdateAdminCredentials changes the admin's login and/or password. Empty username
// or password fields are left unchanged. The current password must be supplied and
// is re-verified first — a stolen session cookie alone must not be enough to rewrite
// the credentials. On success every other session for this admin is revoked (the
// caller's keepToken survives), so a previously stolen cookie can't outlive the
// change.
func (m *Manager) UpdateAdminCredentials(adminID int64, currentPassword, username, password, keepToken string) error {
	username = strings.TrimSpace(username)
	if username == "" && password == "" {
		return invalid("нечего обновлять")
	}
	hash, err := m.store.GetAdminHash(adminID)
	if err != nil {
		return err
	}
	if !auth.VerifyPassword(hash, currentPassword) {
		return invalid("текущий пароль неверен")
	}
	if username != "" {
		if err := m.store.UpdateAdminUsername(adminID, username); err != nil {
			return fmt.Errorf("could not change login (already taken?): %w", err)
		}
	}
	if password != "" {
		if err := m.ChangeAdminPassword(adminID, password); err != nil {
			return err
		}
	}
	return m.store.DeleteSessionsForAdminExcept(adminID, keepToken)
}

// RegenerateSecretPath issues a fresh random panel path and persists it. The
// caller is responsible for swapping the live router. Returns the new path.
func (m *Manager) RegenerateSecretPath() (string, error) {
	p, err := auth.RandomSecretPath()
	if err != nil {
		return "", err
	}
	if err := m.store.SetSecretPath(p); err != nil {
		return "", err
	}
	return p, nil
}

// SetPanelName validates and persists the panel display name (empty ⇒ default).
func (m *Manager) SetPanelName(name string) error {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > branding.MaxNameLen {
		return invalid("название панели не длиннее %d символов", branding.MaxNameLen)
	}
	return m.store.SetPanelName(name)
}

// SetPanelTheme validates and persists the colour theme (each field empty ⇒ the
// matching default applies).
func (m *Manager) SetPanelTheme(t branding.Theme) error {
	js, err := branding.NormalizeTheme(t)
	if err != nil {
		return invalid("%s", err.Error())
	}
	return m.store.SetPanelTheme(js)
}

// SetDecoyTemplate persists the chosen masquerade template (caller swaps the
// live decoy handler).
func (m *Manager) SetDecoyTemplate(name string) error {
	return m.store.SetDecoyTemplate(name)
}

// SetXrayDNS persists the Xray DNS servers and reloads Xray with the new config.
func (m *Manager) SetXrayDNS(dns string) error {
	if err := m.store.SetXrayDNS(strings.TrimSpace(dns)); err != nil {
		return err
	}
	m.TriggerReconcile()
	return nil
}

// Settings returns the current settings row (read-only handlers).
func (m *Manager) Settings() (*model.Settings, error) { return m.store.GetSettings() }

// GeoCategories returns the geosite + geoip category codes from the on-disk
// databases, parsed once and cached (the .dat files only change on refresh).
func (m *Manager) GeoCategories() (geosite, geoip []string, err error) {
	m.geoMu.Lock()
	defer m.geoMu.Unlock()
	if m.geoSite != nil || m.geoIP != nil {
		return m.geoSite, m.geoIP, nil
	}
	gs, gi, err := geo.Categories(m.sup.AssetDir())
	if err != nil {
		return nil, nil, err
	}
	m.geoSite, m.geoIP = gs, gi
	return gs, gi, nil
}

// GeoStatus reports the on-disk state of the geoip/geosite databases (presence,
// size, last-download time) for the settings UI.
func (m *Manager) GeoStatus() []geo.FileInfo { return geo.Status(m.sup.AssetDir()) }

// RefreshGeo re-downloads the geo databases to their latest version, drops the
// parsed-category cache, and reloads Xray so routing rules pick up the new data.
func (m *Manager) RefreshGeo() ([]geo.FileInfo, error) {
	dir := m.sup.AssetDir()
	if err := geo.Refresh(dir); err != nil {
		return geo.Status(dir), err
	}
	m.geoMu.Lock()
	m.geoSite, m.geoIP = nil, nil // force re-parse on next GeoCategories
	m.geoMu.Unlock()
	m.TriggerReconcile() // reload Xray with the refreshed databases
	return geo.Status(dir), nil
}

// SetProxyMode persists the forward-proxy inbound (proxy mode) and reloads Xray.
func (m *Manager) SetProxyMode(enabled bool, typ string, port int, user, pass string) error {
	if typ != "socks" && typ != "http" {
		return invalid("тип прокси должен быть socks или http")
	}
	if port < 1 || port > 65535 {
		return invalid("порт вне диапазона 1–65535")
	}
	user = strings.TrimSpace(user)
	if enabled && (user == "" || pass == "") {
		return invalid("для режима прокси нужны логин и пароль")
	}
	if err := m.store.SetProxyMode(enabled, typ, port, user, pass); err != nil {
		return err
	}
	m.TriggerReconcile()
	return nil
}

// ApplyRouting persists the routing config plus the WARP/Opera on/off state in
// one shot, then reconciles once. The first WARP enable provisions a free WARP
// account (Cloudflare device registration) and caches the WireGuard credentials;
// later toggles reuse them. Enabling Opera downloads + launches the helper for
// the chosen region.
func (m *Manager) ApplyRouting(cfg model.RoutingConfig, warpEnabled, operaEnabled bool, operaCountry string) error {
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	logInfo("routing: applying", "warp", warpEnabled, "opera", operaEnabled, "country", operaCountry)
	set.WarpEnabled = warpEnabled
	if warpEnabled && !set.WarpRegistered() {
		logInfo("warp: registering new Cloudflare WARP account")
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		acc, err := warp.Register(ctx)
		if err != nil {
			logErr("warp: registration failed", "err", err)
			return fmt.Errorf("регистрация WARP не удалась: %w", err)
		}
		set.WarpPrivateKey = acc.PrivateKey
		set.WarpPublicKey = acc.PeerPublicKey
		set.WarpEndpoint = acc.Endpoint
		set.WarpAddressV4 = acc.AddressV4
		set.WarpAddressV6 = acc.AddressV6
		set.WarpReserved = joinInts(acc.Reserved)
	}
	if err := m.store.SetWarp(set); err != nil {
		return err
	}

	// Opera VPN: bring the helper up (or down) BEFORE persisting, so a failed
	// enable aborts without leaving the setting stuck "on" with no proxy behind it.
	set.OperaCountry = operaCountry
	country, port := set.OperaCountryOr(), set.OperaPortOr()
	if err := m.syncOpera(operaEnabled, country, port); err != nil {
		return err
	}
	if err := m.store.SetOpera(operaEnabled, country, port); err != nil {
		return err
	}

	if err := m.store.SetRoutingConfig(cfg); err != nil {
		return err
	}
	// Refresh the proxy pool from the saved sources so the reconcile picks up a
	// changed URL / manual list.
	m.setProxies(m.buildProxies(cfg))
	m.TriggerReconcile()
	// Probe the helper lanes now (off the request path) so their alive/fallback
	// status is fresh when the UI re-fetches after the Xray restart.
	go m.probeLanes()
	return nil
}

// joinInts renders [1,2,3] as "1,2,3" for the warp_reserved column.
func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

// subPathRe validates the public subscription path prefix: URL-path-safe, 1–32 chars.
var subPathRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// reservedSubPaths are first-segment names the subscription prefix must not use:
// they belong to the panel/system surface (the panel mux serves these under the
// secret, and "well-known" is conventionally reserved for ACME), so allowing a
// subscription there would be confusing or could shadow real routes. Matched
// case-insensitively. The secret path itself is checked separately.
var reservedSubPaths = map[string]bool{
	"api":        true,
	"assets":     true,
	"login":      true,
	"logout":     true,
	"favicon":    true,
	"static":     true,
	"well-known": true,
}

// SaveSubSettings validates and persists the subscription delivery settings. The
// subscription path must be URL-safe and must not shadow the secret panel path
// or any reserved panel/system segment.
func (m *Manager) SaveSubSettings(st *model.Settings) error {
	st.SubPath = strings.TrimSpace(st.SubPath)
	if !subPathRe.MatchString(st.SubPath) {
		return invalid("путь подписки: латиница, цифры, «-» и «_», 1–32 символа")
	}
	if reservedSubPaths[strings.ToLower(st.SubPath)] {
		return invalid("путь подписки «%s» зарезервирован панелью — выберите другой", st.SubPath)
	}
	cur, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	if strings.EqualFold(st.SubPath, cur.PanelSecretPath) {
		return invalid("путь подписки не может совпадать с секретным путём панели")
	}
	return m.store.SetSubSettings(st)
}

type routingTmpl struct {
	body string
	at   time.Time
}

// routingTmplTTL is how long a cached routing template is served before it's
// refreshed; routingFetchBudget caps a single fetch so a slow/unreachable GitHub
// can't stall the subscription response (Happ/INCY read the routing header inline).
const (
	routingTmplTTL     = time.Hour
	routingFetchBudget = 4 * time.Second
)

// FetchRoutingTemplate returns the body of a routing-template URL WITHOUT ever
// blocking the caller on a slow remote when a cached copy exists: a fresh entry is
// returned as-is, a stale one is returned immediately while a refresh runs in the
// background (stale-while-revalidate). Only a completely cold cache fetches
// synchronously — and then with a short budget. This is what keeps the Happ/INCY
// subscription pull from timing out when GitHub is slow: previously a cold/stale
// cache forced an inline 8s GET, so the whole subscription response hung.
func (m *Manager) FetchRoutingTemplate(url string) (string, error) {
	if err := netguard.ValidateFetchURL(url); err != nil {
		return "", err
	}
	m.tmplMu.Lock()
	e, ok := m.tmplCache[url]
	m.tmplMu.Unlock()
	if ok {
		if time.Since(e.at) >= routingTmplTTL {
			go m.fetchRoutingTemplate(url) // refresh in the background; serve stale now
		}
		return e.body, nil
	}
	return m.fetchRoutingTemplate(url)
}

// fetchRoutingTemplate performs the HTTP GET (short timeout), caches a good body,
// and falls back to any prior cached copy on error.
func (m *Manager) fetchRoutingTemplate(url string) (string, error) {
	stale := func() (string, bool) {
		m.tmplMu.Lock()
		defer m.tmplMu.Unlock()
		e, ok := m.tmplCache[url]
		return e.body, ok
	}
	ctx, cancel := context.WithTimeout(context.Background(), routingFetchBudget)
	defer cancel()
	b, err := netguard.Get(ctx, url, 1<<20)
	if err != nil {
		if s, ok := stale(); ok {
			return s, nil
		}
		return "", err
	}
	body := string(b)
	m.tmplMu.Lock()
	if m.tmplCache == nil {
		m.tmplCache = make(map[string]routingTmpl)
	}
	m.tmplCache[url] = routingTmpl{body: body, at: time.Now()}
	m.tmplMu.Unlock()
	return body, nil
}

// prewarmRoutingTemplates fetches the configured routing-template URLs once at
// startup (in the background) so the in-memory cache is populated right after a
// restart — otherwise the first Happ/INCY subscription pull would fetch
// synchronously and could time out on a slow GitHub.
func (m *Manager) prewarmRoutingTemplates() {
	set, err := m.store.GetSettings()
	if err != nil || !set.SubRouting {
		return
	}
	for _, url := range []string{set.SubRoutingHapp, set.SubRoutingIncy, set.SubRoutingMihomo} {
		if strings.TrimSpace(url) != "" {
			_, _ = m.fetchRoutingTemplate(url)
		}
	}
}
