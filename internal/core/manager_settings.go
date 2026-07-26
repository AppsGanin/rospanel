package core

import (
	"bytes"
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
	"github.com/AppsGanin/rospanel/internal/logbuf"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/netguard"
	"github.com/AppsGanin/rospanel/internal/warp"
	"github.com/AppsGanin/rospanel/internal/xray"
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
	logbuf.SetLocation(m.tz) // keep log timestamps on the operator's new zone too
	m.tzMu.Unlock()
	return nil
}

// ChangeAdminPassword hashes and stores a new password for the given admin and
// lifts that admin's forced-password-change gate (a password the admin picked
// themselves is exactly what the gate is waiting for).
func (m *Manager) ChangeAdminPassword(adminID int64, newPassword string) error {
	if len(newPassword) < minAdminPassword {
		return invalid("пароль должен быть не короче %d символов", minAdminPassword)
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	return m.store.UpdateAdminPassword(adminID, hash, false)
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

// assetDir is the directory holding the geo + iplist databases, or "" when the
// Manager has no supervisor (unit tests build a bare Manager). The geo readers
// treat "" as "no databases present" and error rather than panicking, so every
// caller degrades to "no categories / no groups" instead of taking the panel down.
func (m *Manager) assetDir() string {
	if m.sup == nil {
		return ""
	}
	return m.sup.AssetDir()
}

// GeoCategories returns the geosite + geoip category codes from the on-disk
// databases, parsed once and cached (the .dat files only change on refresh).
func (m *Manager) GeoCategories() (geosite, geoip []string, err error) {
	m.geoMu.Lock()
	defer m.geoMu.Unlock()
	if m.geoSite != nil || m.geoIP != nil {
		return m.geoSite, m.geoIP, nil
	}
	gs, gi, err := geo.Categories(m.assetDir())
	if err != nil {
		return nil, nil, err
	}
	m.geoSite, m.geoIP = gs, gi
	return gs, gi, nil
}

// GeoGroups returns the iplist groups parsed from the on-disk databases, cached
// like GeoCategories (they only change on a refresh). Callers must not mutate
// the returned set.
func (m *Manager) GeoGroups() (geo.GroupSet, error) {
	m.geoMu.Lock()
	defer m.geoMu.Unlock()
	if m.geoGroups != nil {
		return m.geoGroups, nil
	}
	g, err := geo.Groups(m.assetDir())
	if err != nil {
		return nil, err
	}
	m.geoGroups = g
	return g, nil
}

// genOpts returns the generation options with the iplist groups resolved, so
// "iplist:" routing entries compile to real matchers. A parse failure (databases
// not downloaded yet) degrades to no groups rather than blocking generation —
// those rules are skipped and their traffic falls through to the next lane.
func (m *Manager) genOpts() xray.Options {
	opts := m.opts
	if g, err := m.GeoGroups(); err == nil {
		opts.Groups = g
	}
	return opts
}

// genOptsFor is genOpts plus the per-server pieces: the server id, its custom
// inbounds, and the per-user access map that gates which lanes each user's credential
// is written into.
//
// The two reads fail differently ON PURPOSE. A custom-inbounds read failure is soft:
// generation proceeds with no custom inbounds, because the built-in lanes are what keep
// the server reachable and losing a lane cosmetically beats an outage. An ACCESS read
// failure is HARD (returns an error): the access map decides which users are withheld
// from which lanes, so generating without it would write every restricted user's
// credential into every lane — a security regression baked into the live config until
// the next reconcile. Failing the reconcile instead keeps the previous, correctly
// gated config in force. (The subscription path reads per-user and can fail soft — it
// is read-only and self-corrects on the next fetch; here we are about to persist.)
func (m *Manager) genOptsFor(serverID int64) (xray.Options, error) {
	opts := m.genOpts()
	opts.ServerID = serverID
	access, err := m.store.AccessMap()
	if err != nil {
		return opts, fmt.Errorf("load access map: %w", err)
	}
	opts.Access = access
	list, err := m.store.EnabledInbounds(serverID)
	if err != nil {
		logErr("inbounds: load failed", "server", serverID, "err", err)
		return opts, nil
	}
	opts.Custom = list
	return opts, nil
}

// GeoStatus reports the on-disk state of the Xray geo databases (presence, size,
// last-download time) for the settings UI.
func (m *Manager) GeoStatus() []geo.FileInfo { return geo.Status(m.assetDir()) }

// IPListStatus reports the on-disk state of the iplist databases. Separate from
// GeoStatus because they are a separate concern with their own panel tab: Xray
// reads the geo .dat files, while the iplist lists are the panel's own source for
// "iplist:" rules.
func (m *Manager) IPListStatus() []geo.FileInfo { return geo.StatusLists(m.assetDir()) }

// dropGeoCache forces a re-parse of the categories and groups on next use. Called
// after every refresh — including a partial failure, since each file is written
// atomically and independently, so whatever did land must be picked up.
func (m *Manager) dropGeoCache() {
	m.geoMu.Lock()
	m.geoSite, m.geoIP, m.geoGroups = nil, nil, nil
	m.geoMu.Unlock()
}

// RefreshGeo re-downloads the Xray geo databases to their latest version, drops
// the parsed caches, and reloads Xray so routing rules pick up the new data.
func (m *Manager) RefreshGeo() ([]geo.FileInfo, error) {
	if err := geo.Refresh(m.assetDir()); err != nil {
		return m.GeoStatus(), err
	}
	m.dropGeoCache()
	m.TriggerReconcile()
	return m.GeoStatus(), nil
}

// RefreshIPLists re-downloads the iplist databases, drops the parsed caches and
// reloads Xray, so a changed group takes effect at once.
func (m *Manager) RefreshIPLists() ([]geo.FileInfo, error) {
	if err := geo.RefreshLists(m.assetDir()); err != nil {
		return m.IPListStatus(), err
	}
	m.dropGeoCache()
	m.TriggerReconcile()
	return m.IPListStatus(), nil
}

// GeoRefreshHours returns the configured geo auto-refresh cadence (hours; 0 ⇒ off).
func (m *Manager) GeoRefreshHours() int {
	set, err := m.store.GetSettings()
	if err != nil {
		return 0
	}
	return set.GeoRefreshHours
}

// currentGeoRefresh reads the geo auto-refresh cadence as a duration (0 ⇒ off).
func (m *Manager) currentGeoRefresh() time.Duration {
	set, err := m.store.GetSettings()
	if err != nil || set.GeoRefreshHours <= 0 {
		return 0
	}
	return time.Duration(set.GeoRefreshHours) * time.Hour
}

// IPListRefreshHours returns the configured iplist auto-refresh cadence (hours;
// 0 ⇒ off).
func (m *Manager) IPListRefreshHours() int {
	set, err := m.store.GetSettings()
	if err != nil {
		return 0
	}
	return set.IPListRefreshHours
}

// currentIPListRefresh reads the iplist auto-refresh cadence as a duration (0 ⇒ off).
func (m *Manager) currentIPListRefresh() time.Duration {
	set, err := m.store.GetSettings()
	if err != nil || set.IPListRefreshHours <= 0 {
		return 0
	}
	return time.Duration(set.IPListRefreshHours) * time.Hour
}

// stale reports whether any file in the set is missing or older than maxAge.
func stale(files []geo.FileInfo, maxAge time.Duration) bool {
	cutoff := time.Now().Add(-maxAge).Unix()
	for _, f := range files {
		if !f.Present || f.ModifiedAt < cutoff {
			return true
		}
	}
	return false
}

// geoStale reports whether any geo database is missing or older than maxAge.
func (m *Manager) geoStale(maxAge time.Duration) bool { return stale(m.GeoStatus(), maxAge) }

// ipListStale reports whether any iplist database is missing or older than maxAge.
func (m *Manager) ipListStale(maxAge time.Duration) bool { return stale(m.IPListStatus(), maxAge) }

// geoLoop auto-refreshes the geo databases when they go stale, on the operator's
// cadence (0 ⇒ off). It re-checks hourly so a cadence change takes effect without a
// restart and a reboot doesn't reset a long timer. Sleeps first so boot stays quiet;
// enabling the cadence refreshes promptly via SetGeoRefresh.
func (m *Manager) geoLoop() {
	refreshLoop("geo", m.currentGeoRefresh, m.geoStale, func() error {
		_, err := m.RefreshGeo()
		return err
	})
}

// ipListLoop is geoLoop's twin for the iplist databases, on their OWN cadence —
// they follow a different upstream clock (~12h) and are panel-only, so tying them
// to the geo schedule would either poll the lists too rarely or drag ~28 MB of
// .dat files down far too often.
func (m *Manager) ipListLoop() {
	refreshLoop("iplist", m.currentIPListRefresh, m.ipListStale, func() error {
		_, err := m.RefreshIPLists()
		return err
	})
}

// refreshLoop is the shared hourly staleness poll behind geoLoop/ipListLoop.
func refreshLoop(what string, cadence func() time.Duration, isStale func(time.Duration) bool, refresh func() error) {
	for {
		time.Sleep(time.Hour)
		d := cadence()
		if d <= 0 || !isStale(d) {
			continue
		}
		if err := refresh(); err != nil {
			logWarn(what+": auto-refresh failed", "err", err)
		} else {
			logInfo(what+": auto-refreshed", "cadence_hours", int(d/time.Hour))
		}
	}
}

// SetGeoRefresh persists the geo auto-refresh cadence (hours; 0 ⇒ never) and, if
// enabling with geo already stale, refreshes right away instead of waiting for the
// loop's next tick.
func (m *Manager) SetGeoRefresh(hours int) error {
	if hours < 0 {
		hours = 0
	}
	if err := m.store.SetGeoRefresh(hours); err != nil {
		return err
	}
	if d := time.Duration(hours) * time.Hour; d > 0 && m.geoStale(d) {
		go func() {
			if _, err := m.RefreshGeo(); err != nil {
				logWarn("geo: refresh on enable failed", "err", err)
			}
		}()
	}
	return nil
}

// SetIPListRefresh persists the iplist auto-refresh cadence (hours; 0 ⇒ never),
// refreshing at once if enabling with the lists already stale.
func (m *Manager) SetIPListRefresh(hours int) error {
	if hours < 0 {
		hours = 0
	}
	if err := m.store.SetIPListRefresh(hours); err != nil {
		return err
	}
	if d := time.Duration(hours) * time.Hour; d > 0 && m.ipListStale(d) {
		go func() {
			if _, err := m.RefreshIPLists(); err != nil {
				logWarn("iplist: refresh on enable failed", "err", err)
			}
		}()
	}
	return nil
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
	// Fold a legacy single-pool payload (an older panel build) into a lane, then
	// validate — so what we persist is always in the lane model.
	cfg.MigrateLanes()
	if err := cfg.ValidateLanes(); err != nil {
		return invalid("%s", err)
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	logInfo("routing: applying", "warp", warpEnabled, "opera", operaEnabled, "country", operaCountry, "lanes", len(cfg.Lanes))
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

// announceMaxRunes is the cap VPN clients themselves impose on the announcement
// they display (Happ documents 200; Remnawave validates the same number). Anything
// past it is cut off client-side, so the panel refuses it rather than let an
// operator send half a sentence.
const announceMaxRunes = 200

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
	st.SubAnnounce = strings.TrimSpace(st.SubAnnounce)
	// Clients render at most 200 characters of the announcement and silently cut the
	// rest, so a longer text is a message the operator thinks they sent and nobody
	// ever read. Reject it here instead. Runes, not bytes: the text is Cyrillic.
	if n := utf8.RuneCountInString(st.SubAnnounce); n > announceMaxRunes {
		return invalid("объявление: не длиннее %d символов (сейчас %d) — клиенты обрежут остальное",
			announceMaxRunes, n)
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
			go func() { _, _ = m.fetchRoutingTemplate(url) }() // refresh in the background; serve stale now
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

const (
	// telegramSDKURL is Telegram's official Mini App JS wrapper. The subscription
	// page loads it from OUR origin (a cached copy of this) instead of directly:
	// telegram.org is blocked in Russia, so a direct <script> would hang the page
	// for the whole connection timeout before painting.
	telegramSDKURL      = "https://telegram.org/js/telegram-web-app.js"
	telegramSDKTTL      = 24 * time.Hour  // how long a cached copy is served before a refresh
	telegramSDKBudget   = 5 * time.Second // cap on a single upstream fetch, inline ones included
	telegramSDKRetryGap = time.Minute     // after a failed fetch, don't stall a page again this soon
	telegramSDKMaxBytes = 1 << 20         // the wrapper is ~120 KB; 1 MiB is ample headroom
)

// telegramSDKMarker must appear in a fetched body for it to be cached. netguard
// already rejects non-200 and enforces https, but a transparent proxy answering 200
// with an HTML block page would otherwise be cached as JS for a full TTL and served
// to every user. It also catches a silent truncation at telegramSDKMaxBytes (which
// returns no error). The real wrapper mentions Telegram.WebApp ~115 times.
var telegramSDKMarker = []byte("Telegram.WebApp")

// TelegramWebAppSDK returns a server-side cached copy of telegram.org's
// telegram-web-app.js so the subscription page can serve it from our own
// (reachable) origin. A fresh copy is returned as-is; a stale one is served
// immediately while a refresh runs behind it (stale-while-revalidate), so a page
// load never waits on a copy we already have.
//
// A COLD cache fetches inline, so the first visitor still gets the real SDK rather
// than an empty file. That is the one place this can make a page wait, and it is
// bounded on both axes: telegramSDKBudget caps the single fetch, and a failure arms
// a telegramSDKRetryGap cooldown during which cold reads return immediately. So an
// unreachable telegram.org costs one bounded wait per cooldown, not one per page
// load — which is what keeps this from reintroducing the very hang the proxy exists
// to remove. ok=false means "serve an empty body"; the page treats a missing SDK as
// "not in Telegram" and renders normally.
func (m *Manager) TelegramWebAppSDK() ([]byte, bool) {
	m.tgSDKMu.Lock()
	if body := m.tgSDKBody; body != nil {
		stale := time.Since(m.tgSDKAt) >= telegramSDKTTL
		m.tgSDKMu.Unlock()
		if stale {
			go m.refreshTelegramSDK() // serve what we have now, refresh behind it
		}
		return body, true
	}
	if time.Since(m.tgSDKFailAt) < telegramSDKRetryGap {
		m.tgSDKMu.Unlock() // upstream just failed us; don't stall this page too
		return nil, false
	}
	wait, lead := m.tgSDKWait, false
	if wait == nil { // nobody is fetching — this request does it
		wait = make(chan struct{})
		m.tgSDKWait, lead = wait, true
	}
	m.tgSDKMu.Unlock()

	if lead {
		m.fetchTelegramSDK()
	} else {
		// A fetch is already in flight: ride along instead of starting a second one.
		select {
		case <-wait:
		case <-time.After(telegramSDKBudget):
			return nil, false
		}
	}

	m.tgSDKMu.Lock()
	body := m.tgSDKBody
	m.tgSDKMu.Unlock()
	return body, body != nil
}

// telegramSDKFetch performs the upstream GET. It's a var so tests can drive the
// cache logic without a network (netguard rejects loopback, so httptest is out).
var telegramSDKFetch = func(ctx context.Context) ([]byte, error) {
	return netguard.Get(ctx, telegramSDKURL, telegramSDKMaxBytes)
}

// refreshTelegramSDK refreshes a stale copy in the background (the only caller —
// there is no startup warm-up, see New). It's a no-op while a fetch is in flight OR
// while the failure cooldown is armed.
//
// That cooldown is what stops a failing upstream from becoming a retry loop: a
// failed fetch never advances tgSDKAt, so a stale copy stays stale and EVERY
// subsequent request re-triggers this. Without the guard the panel would dial a
// blocked telegram.org back-to-back for as long as the page saw traffic — a beacon
// the decoy story does not cover.
func (m *Manager) refreshTelegramSDK() {
	m.tgSDKMu.Lock()
	if m.tgSDKWait != nil || time.Since(m.tgSDKFailAt) < telegramSDKRetryGap {
		m.tgSDKMu.Unlock()
		return
	}
	m.tgSDKWait = make(chan struct{})
	m.tgSDKMu.Unlock()
	m.fetchTelegramSDK()
}

// fetchTelegramSDK performs the upstream GET and publishes the result, then releases
// everyone waiting on it. The caller must have claimed the fetch by setting
// tgSDKWait. A failed fetch keeps any previous cached copy and stamps tgSDKFailAt so
// readers stop blocking for a while.
//
// Publish/release runs in a defer so a panic anywhere in the fetch stack can't latch
// tgSDKWait non-nil forever — that would permanently disable refreshes AND make every
// later reader wait out the full budget as a rider on a fetch that never completes.
func (m *Manager) fetchTelegramSDK() {
	var (
		b   []byte
		err error
	)
	defer func() {
		m.tgSDKMu.Lock()
		if err == nil && bytes.Contains(b, telegramSDKMarker) {
			m.tgSDKBody, m.tgSDKAt = b, time.Now()
			m.tgSDKFailAt = time.Time{}
		} else {
			m.tgSDKFailAt = time.Now()
		}
		if m.tgSDKWait != nil {
			close(m.tgSDKWait) // wake the riders; they re-read the cache
			m.tgSDKWait = nil
		}
		m.tgSDKMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), telegramSDKBudget)
	defer cancel()
	b, err = telegramSDKFetch(ctx)
}
