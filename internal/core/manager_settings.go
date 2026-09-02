package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
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
			return invalidCode("err.unknownTimezone", "неизвестный часовой пояс {{value}}", map[string]any{"value": name})
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
		return invalidCode("err.passwordTooShort", "пароль должен быть не короче {{min}} символов", map[string]any{"min": minAdminPassword})
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
		return invalidCode("err.nothingToUpdate", "нечего обновлять")
	}
	hash, err := m.store.GetAdminHash(adminID)
	if err != nil {
		return err
	}
	if !auth.VerifyPassword(hash, currentPassword) {
		return invalidCode("err.wrongCurrentPassword", "текущий пароль неверен")
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
		return invalidCode("err.panelNameTooLong", "название панели не длиннее {{max}} символов", map[string]any{"max": branding.MaxNameLen})
	}
	return m.store.SetPanelName(name)
}

// SetPanelTheme validates and persists the colour theme (each field empty ⇒ the
// matching default applies).
func (m *Manager) SetPanelTheme(t branding.Theme) error {
	js, err := branding.NormalizeTheme(t)
	if err != nil {
		return fromFieldErr(err)
	}
	return m.store.SetPanelTheme(js)
}

// SetDecoyTemplate persists the chosen masquerade template (caller swaps the
// live decoy handler).
func (m *Manager) SetDecoyTemplate(name string) error {
	return m.store.SetDecoyTemplate(name)
}

// SetXrayDNS persists the Xray DNS servers and reloads Xray with the new config.
//
// The entries are validated HERE rather than in the panel handler that used to own the
// check: /v1 reaches this too, and a DNS server only the panel refused would be stored
// by the API and then reconciled against — Xray would take a config the operator could
// not have produced through the UI.
func (m *Manager) SetXrayDNS(dns string) error {
	for _, e := range strings.FieldsFunc(dns, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ' '
	}) {
		if !validDNSServer(e) {
			return invalidCode("err.badDNS", "неверный DNS-адрес: {{detail}}", map[string]any{"detail": e})
		}
	}
	if err := m.store.SetXrayDNS(strings.TrimSpace(dns)); err != nil {
		return err
	}
	m.TriggerReconcile()
	return nil
}

// validDNSServer accepts a plain IP, an ip:port, a DoH/DoT URL, or "localhost".
func validDNSServer(s string) bool {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return false
	case s == "localhost":
		return true
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		return err == nil && u.Host != ""
	case net.ParseIP(s) != nil:
		return true
	default:
		host, _, err := net.SplitHostPort(s)
		return err == nil && net.ParseIP(host) != nil
	}
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
// gated config in force. The subscription path now refuses for the same reason rather
// than degrading to unrestricted (see server.subServers): a read-only surface that hands
// out the address of every lane is still handing out what the reader may not have.
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
		// The IP→ASN table is panel-only on a similar clock; refresh it on the same tick
		// rather than giving it a cadence of its own. Best-effort — a stale ASN table
		// still resolves, it just misses recently-reassigned ranges.
		if e := geo.RefreshASN(m.assetDir()); e != nil {
			logWarn("asn: auto-refresh failed", "err", e)
		}
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

// SetSystemProxy configures one server's forward-proxy listeners — serverID 0 is the
// panel's own machine, anything else a node. One entry point for both because the
// two differ only in where the row lives: the validation, the port collisions and the
// consequence (this server's Xray gains or loses a public listener) are identical.
func (m *Manager) SetSystemProxy(serverID int64, p model.SystemProxy) error {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return fromFieldErr(err)
	}
	set, err := m.serverSettings(serverID)
	if err != nil {
		return err
	}
	// Collisions are checked against THIS server's other listeners: a node's ports
	// have nothing to do with the master's.
	if err := m.checkProxyPorts(p, set, m.serverInbounds(serverID)); err != nil {
		return err
	}
	if serverID == model.LocalNodeID {
		if err := m.store.SetSystemProxy(p); err != nil {
			return err
		}
		m.TriggerReconcile()
		return nil
	}
	if err := m.store.SetNodeSystemProxy(serverID, p); err != nil {
		return err
	}
	// A node applies whatever config the panel hands it on its next poll; waking it
	// makes that now rather than up to 45s from now.
	m.nodes.wakeOne(serverID)
	return nil
}

// serverSettings materializes one server's effective settings (the master's own, or a
// node's after nodeSettings), so callers can reason about a single server uniformly.
func (m *Manager) serverSettings(serverID int64) (*model.Settings, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	if serverID == model.LocalNodeID {
		return set, nil
	}
	n, err := m.store.GetNode(serverID)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, invalidCode("err.nodeNotFound", "сервер не найден")
	}
	return nodeSettings(set, n), nil
}

// serverInbounds is one server's custom inbounds, best-effort: a read failure here
// costs a port-collision check, not the write.
func (m *Manager) serverInbounds(serverID int64) []model.Inbound {
	ins, err := m.store.Inbounds(serverID)
	if err != nil {
		return nil
	}
	return ins
}

// checkProxyPorts rejects a proxy port already held by one of this server's other
// listeners — a built-in lane, a custom inbound, WARP's loopback entrance. Xray would
// otherwise start, fail to bind the second listener, and take the whole config down
// with it.
func (m *Manager) checkProxyPorts(p model.SystemProxy, set *model.Settings, custom []model.Inbound) error {
	// Computed with the proxy's OWN ports stripped out, so re-saving an unchanged
	// configuration doesn't report it colliding with itself.
	reserved := otherListenerPorts(set, custom)
	m.holdPanelPort(reserved)
	if p.SocksEnabled {
		if who, taken := reserved[p.SocksPort]; taken {
			return invalidCode("err.portTaken", "порт {{port}} уже занят: {{who}}",
				map[string]any{"port": p.SocksPort, "who": who})
		}
	}
	if p.HTTPEnabled {
		if who, taken := reserved[p.HTTPPort]; taken {
			return invalidCode("err.portTaken", "порт {{port}} уже занят: {{who}}",
				map[string]any{"port": p.HTTPPort, "who": who})
		}
	}
	return nil
}

// otherListenerPorts is this server's held ports — its built-in lanes, custom inbounds
// and loopback machinery — WITHOUT the system proxy's own, which would otherwise make
// every re-save of an unchanged configuration collide with itself.
func otherListenerPorts(set *model.Settings, custom []model.Inbound) model.ReservedPorts {
	stripped := *set
	stripped.ProxySocksPort, stripped.ProxyHTTPPort = 0, 0
	r := reservedPorts(&stripped)
	for _, in := range custom {
		if in.Port > 0 {
			r[in.Port] = in.Name
		}
	}
	return r
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
		return fromFieldErr(err)
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
			return fmt.Errorf("WARP registration failed: %w", err)
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
		return invalidCode("err.subPathCharset", "путь подписки: латиница, цифры, «-» и «_», 1–32 символа")
	}
	st.SubAnnounce = strings.TrimSpace(st.SubAnnounce)
	if st.SubOrderMode != "" && !model.ValidOrderMode(st.SubOrderMode) {
		return invalidCode("err.subOrderMode", "неизвестный режим порядка серверов")
	}
	// Clients render at most 200 characters of the announcement and silently cut the
	// rest, so a longer text is a message the operator thinks they sent and nobody
	// ever read. Reject it here instead. Runes, not bytes: the text is Cyrillic.
	if n := utf8.RuneCountInString(st.SubAnnounce); n > announceMaxRunes {
		return invalidCode("err.announceTooLong",
			"объявление: не длиннее {{max}} символов (сейчас {{count}}) — клиенты обрежут остальное",
			map[string]any{"max": announceMaxRunes, "count": n})
	}
	if reservedSubPaths[strings.ToLower(st.SubPath)] {
		return invalidCode("err.subPathReserved", "путь подписки «{{path}}» зарезервирован панелью — выберите другой", map[string]any{"path": st.SubPath})
	}
	cur, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	if strings.EqualFold(st.SubPath, cur.PanelSecretPath) {
		return invalidCode("err.subPathSameAsPanel", "путь подписки не может совпадать с секретным путём панели")
	}
	return m.store.SetSubSettings(st)
}

// ConfigSnapshots lists the server-config snapshots (newest first).
func (m *Manager) ConfigSnapshots() ([]model.ConfigSnapshot, error) {
	return m.store.ListConfigSnapshots()
}

// snapshotCurrentConfig captures the master's whole server config into a snapshot.
func (m *Manager) snapshotCurrentConfig(label string, auto bool) (int64, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return 0, err
	}
	inbounds, err := m.store.Inbounds(model.LocalNodeID)
	if err != nil {
		return 0, err
	}
	blob, err := json.Marshal(model.ServerConfigFrom(set, inbounds))
	if err != nil {
		return 0, err
	}
	return m.store.CreateConfigSnapshot(strings.TrimSpace(label), auto, string(blob))
}

// SnapshotServerConfig takes an operator's manual save-point of the whole server config
// and returns its id, so a caller can name the save-point it just made rather than
// guessing at "the newest one".
func (m *Manager) SnapshotServerConfig(label string) (int64, error) {
	return m.snapshotCurrentConfig(label, false)
}

// RollbackServerConfig restores a snapshot's server config: everything on the
// server-settings tabs (protocols, ports, REALITY, routing, egress, DNS, decoy,
// inbounds) EXCEPT the certificate/domain identity, which is left as-is so the live
// cert is never broken by an undo. The current config is auto-snapshotted first, so a
// rollback is itself undoable; the whole thing then reconciles (regenerate + validate
// via xray -test), so a config the snapshot's values can't produce is caught.
func (m *Manager) RollbackServerConfig(id int64) error {
	cfg, err := m.store.ConfigSnapshot(id)
	if err != nil {
		return invalidCode("err.snapshotNotFound", "снимок не найден")
	}
	// Undo point for the rollback itself. Best-effort — a failed pre-snapshot must not
	// block the restore the operator asked for.
	_, _ = m.snapshotCurrentConfig("", true)

	// Settings + custom inbounds restore atomically (see store.RestoreServerConfig):
	// inbounds keep their original ids so group grants survive, and a failure rolls the
	// whole thing back rather than leaving a half-restored config.
	if err := m.store.RestoreServerConfig(cfg); err != nil {
		return err
	}

	// A snapshot restores ports, hop ranges, the Opera backend and the decoy — and every
	// one of those has a HOST-level half that lives outside the Xray config. Regenerating
	// alone left the box in the state the panel could not explain: nftables still
	// funnelling the old UDP range onto the old port, the per-IP flood guard still
	// protecting the old port set (a re-enabled REALITY port coming up unguarded), Opera
	// enabled in the config with no helper listening — or still running after being
	// switched off — and the panel serving the pre-rollback masquerade. Each of these is
	// what the ordinary write path for that setting does after saving it.
	if err := EnsureHostHops(m.store); err != nil {
		logErr("snapshot: re-applying port hops after a rollback failed", "err", err)
	}
	m.ensureLocalConnGuard()
	set, err := m.store.GetSettings()
	if err == nil {
		if oerr := m.syncOpera(set.OperaEnabled, set.OperaCountryOr(), set.OperaPortOr()); oerr != nil {
			logErr("snapshot: re-applying the Opera backend after a rollback failed", "err", oerr)
		}
	}
	// The live decoy handler belongs to the HTTP router, which the manager cannot reach;
	// the handlers call OnDecoyChange after this returns.
	m.TriggerReconcile()
	return nil
}

// DeleteConfigSnapshot removes one snapshot.
func (m *Manager) DeleteConfigSnapshot(id int64) error {
	return m.store.DeleteConfigSnapshot(id)
}

// SetMaintenanceMode toggles the public-surface maintenance page.
func (m *Manager) SetMaintenanceMode(on bool) error {
	return m.store.SetMaintenanceMode(on)
}

// SetProbeDetect toggles secret-path probe detection.
func (m *Manager) SetProbeDetect(on bool) error {
	if err := m.store.SetProbeDetect(on); err != nil {
		return err
	}
	// Auto-blocking rides on detection: with detection off nothing new is ever flagged,
	// so leaving the firewall table in place would keep dropping every IP blocked
	// earlier — permanently, and with no way back, because the panel hides the
	// auto-block switch (the only thing that calls Clear) whenever detection is off. An
	// operator who turns the feature off is entitled to assume it stopped acting.
	if !on {
		_ = m.probeBlock.Clear()
		return nil
	}
	// Back on: re-arm only if auto-blocking itself is still switched on, since Clear
	// disarmed it above.
	if set, err := m.store.GetSettings(); err == nil && set.ProbeBlock {
		m.probeBlock.Arm()
	}
	return nil
}

// SetWatchdog toggles the wedged-process auto-recovery, persisting it and applying it
// to the running supervisor live.
func (m *Manager) SetWatchdog(on bool) error {
	if err := m.store.SetWatchdogEnabled(on); err != nil {
		return err
	}
	m.sup.SetWatchdogEnabled(on)
	return nil
}

// WatchdogInfo is the operator-facing state of the auto-recovery: on/off, how many
// times it has restarted a wedged Xray, and when it last did (0 = never).
type WatchdogInfo struct {
	Enabled  bool  `json:"enabled"`
	Restarts int   `json:"restarts"`
	LastAt   int64 `json:"last_at"`
}

// Watchdog returns the current auto-recovery state for the settings page.
func (m *Manager) Watchdog() WatchdogInfo {
	enabled, restarts, last := m.sup.WatchdogStats()
	var lastAt int64
	if !last.IsZero() {
		lastAt = last.Unix()
	}
	return WatchdogInfo{Enabled: enabled, Restarts: restarts, LastAt: lastAt}
}

// RecordProbe persists one detected scan (best-effort — a lost row must never affect
// the request that triggered it). paths is how many distinct missing paths the IP hit.
func (m *Manager) RecordProbe(ip string, paths int) {
	if err := m.store.RecordProbe(ip, paths, time.Now().Unix()); err != nil {
		logErr("probe: record failed", "ip", ip, "err", err)
	}
	// If auto-blocking is on, drop the scanner at the firewall too. Best-effort and
	// gated so recording stays the default; a lost block must not affect the request.
	if set, err := m.store.GetSettings(); err == nil && set.ProbeBlock {
		if err := m.probeBlock.BlockIP(ip); err != nil {
			logErr("probe: firewall block failed", "ip", ip, "err", err)
		}
	}
}

// SetProbeBlock toggles firewall auto-blocking of flagged scanner IPs. Turning it off
// tears down the block table (and disarms, so a racing in-flight block can't rebuild it)
// so nothing stays blocked after the operator disables it; turning it on re-arms.
func (m *Manager) SetProbeBlock(on bool) error {
	if err := m.store.SetProbeBlock(on); err != nil {
		return err
	}
	if on {
		m.probeBlock.Arm()
	} else {
		_ = m.probeBlock.Clear()
	}
	return nil
}

// Probes returns the IPs caught scanning for the hidden panel, most recent first,
// each annotated with where it belongs.
func (m *Manager) Probes(limit int) ([]model.ProbeHit, error) {
	probes, err := m.store.ListProbes(limit)
	if err != nil {
		return nil, err
	}
	m.annotateProbes(probes)
	return probes, nil
}

// SubRules returns the stored subscription response rules.
func (m *Manager) SubRules() ([]model.SubRule, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	return set.SubRules, nil
}

// SaveSubRules validates and stores the subscription response rules. Every rule is
// checked so a malformed one (bad regex, unknown field) is rejected up front rather
// than sitting in the list never matching.
func (m *Manager) SaveSubRules(rules []model.SubRule) error {
	for i, r := range rules {
		if err := r.Valid(); err != nil {
			return err
		}
		rules[i].Value = strings.TrimSpace(r.Value)
	}
	return m.store.SetSubRules(rules)
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
	telegramSDKLogGap   = time.Hour       // rate limit on the "fetch failed" log line (retries are far more frequent)
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

// telegramSDKFetch performs the upstream GET, through the operator's Telegram proxy
// when one is set (empty = direct). It's a var so tests can drive the cache logic
// without a network (netguard rejects loopback, so httptest is out).
//
// The proxy matters most precisely here. A server that cannot reach Telegram is the
// only one that ever fails this fetch, and it is also the one whose operator has
// configured a proxy to fix exactly that.
var telegramSDKFetch = func(ctx context.Context, proxy string) ([]byte, error) {
	return netguard.GetVia(ctx, telegramSDKURL, telegramSDKMaxBytes, proxy)
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
		// Decided inside the defer while the mutex is held, acted on by the logging
		// switch once it is released.
		landed, recovered, shout bool
	)
	defer func() {
		m.tgSDKMu.Lock()
		if landed = err == nil && bytes.Contains(b, telegramSDKMarker); landed {
			// tgSDKLogAt non-zero means we complained about this being broken, so the
			// operator is owed the "it works again" line.
			recovered = !m.tgSDKLogAt.IsZero()
			m.tgSDKBody, m.tgSDKAt = b, time.Now()
			m.tgSDKFailAt, m.tgSDKLogAt = time.Time{}, time.Time{}
		} else {
			m.tgSDKFailAt = time.Now()
			// Log the reason, but not on every attempt: a blocked telegram.org fails
			// once per telegramSDKRetryGap for as long as the page sees traffic, and
			// that would push everything else out of the 1000-line dashboard ring
			// inside a day. One line per telegramSDKLogGap is enough to diagnose it.
			if shout = time.Since(m.tgSDKLogAt) >= telegramSDKLogGap; shout {
				m.tgSDKLogAt = time.Now()
			}
		}
		if m.tgSDKWait != nil {
			close(m.tgSDKWait) // wake the riders; they re-read the cache
			m.tgSDKWait = nil
		}
		m.tgSDKMu.Unlock()

		// Logging happens after the unlock: a slog handler can write to disk, and the
		// riders released just above are waiting to take this same mutex.
		//
		// The two failure branches cost the operator the same thing — /tg.js goes out
		// empty, so window.Telegram never appears and the "open in app" buttons stop
		// working inside the Telegram Mini App — so both spell that out. They differ
		// only in cause, which is exactly what the operator can't see from outside.
		switch {
		case landed:
			if recovered {
				logInfo("telegram mini app sdk reachable again", "bytes", len(b))
			}
		case !shout: // same failure already reported inside the log gap
		case err != nil:
			logWarn("telegram mini app sdk fetch failed; /tg.js will be served empty and in-app deep links will not work",
				"url", telegramSDKURL, "err", err)
		default:
			logWarn("telegram mini app sdk fetch returned an unexpected body (blocked or truncated); /tg.js will be served empty and in-app deep links will not work",
				"url", telegramSDKURL, "bytes", len(b))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), telegramSDKBudget)
	defer cancel()
	// Read fresh rather than caching the proxy on the Manager: this runs at most once
	// per cooldown, so one settings read is nothing, and it means a just-saved proxy
	// takes effect on the next page load instead of after a restart.
	var proxy string
	if set, serr := m.store.GetSettings(); serr == nil {
		proxy = set.TelegramProxyURL()
	}
	b, err = telegramSDKFetch(ctx, proxy)
}
