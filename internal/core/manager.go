// Package core ties the store and the Xray supervisor together: mutations go
// through it so the proxy config is reconciled from the DB after every change.
package core

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AppsGanin/rospanel/internal/logbuf"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/opera"
	"github.com/AppsGanin/rospanel/internal/proxypool"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/sysstat"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// TLSPaths are the on-disk locations the panel manages for TLS material.
type TLSPaths struct {
	CertPath string
	KeyPath  string
	ACMEDir  string
}

// reconcileDebounce coalesces bursts of changes into one Xray reload, and — by
// running the reload AFTER the triggering HTTP response is sent — keeps the
// admin's request (which flows through Xray) from being killed by the restart.
const reconcileDebounce = 800 * time.Millisecond

// accLast is pruned of entries older than accLastTTL once it grows past
// accLastMax, so the access throttle map stays bounded to recently-active
// user+IP pairs instead of leaking one entry per pair ever seen.
const (
	accLastMax = 4096
	accLastTTL = int64(time.Hour / time.Second)
)

// Manager is the application service layer.
type Manager struct {
	store       *store.Store
	sup         *xray.Supervisor
	opts        xray.Options
	tls         TLSPaths
	reconcileCh chan struct{}
	// structuralPending marks the next queued reload as a full restart (config
	// changed), vs a cheap live user-sync. Set by TriggerReconcile.
	structuralPending atomic.Bool

	accMu   sync.Mutex
	accLast map[string]int64 // throttle key "uN|ip" → last recorded unix

	// applyMu serializes config application (Reconcile + the live user-sync) so a
	// direct Reconcile (e.g. from tlsLoop on cert renewal) can't interleave with the
	// reconcile-loop's user-sync and leave config.json / the applied set divergent.
	applyMu sync.Mutex

	appliedMu sync.Mutex
	applied   map[int64]struct{} // user IDs currently in the applied config

	tzMu sync.RWMutex
	tz   *time.Location // operator timezone for the local-day stats boundary

	sys *sysstat.Sampler // host metrics sampler for the dashboard (nil until started)

	tmplMu    sync.Mutex
	tmplCache map[string]routingTmpl // cached routing templates by URL

	// userNotify pushes a message to a VPN user's Telegram chat (set by the user
	// bot; nil when off); adminNotify broadcasts to the admin chats (set by the
	// admin bot). Used e.g. to report payment start/completion.
	notifyMu    sync.Mutex
	userNotify  func(chatID int64, html string)
	adminNotify func(html string)

	// notifyThrottle bounds the rate of repeatable system alerts (Xray crash loop,
	// cert renewal errors) so a stuck condition can't flood the admin chats.
	throttleMu        sync.Mutex
	lastCrashNotify   time.Time
	lastCertErrNotify time.Time

	// applyPlanMu serializes ApplyPlanToUser so the read-modify-write of expire_at
	// (base = current expiry, expire = base + period) can't be raced by two
	// concurrent confirmers — a webhook + the poll fallback, or two orders for the
	// same user — which would otherwise lose or double a paid period.
	applyPlanMu sync.Mutex

	vpnMu       sync.Mutex
	vpnUp       int64 // current VPN throughput (bytes/sec), from Xray stats deltas
	vpnDown     int64
	lastVPNUp   int64
	lastVPNDown int64
	lastVPNT    time.Time
	vpnViewers  atomic.Int32 // active dashboard-stream subscribers; gates vpnSpeedLoop

	geoMu   sync.Mutex
	geoSite []string // cached geosite category codes
	geoIP   []string // cached geoip category codes

	proxyMu sync.Mutex
	proxies []model.ProxyEndpoint // current proxy-pool egress endpoints

	guard *bruteGuard

	// connGuardWanted records whether the operator asked for the per-IP connection
	// guard (ROSPANEL_CONNLIMIT != off). Needed to tell "off on purpose" apart from
	// "on, but nftables silently refused it" in the health report — the second is a
	// problem, the first isn't. Set once at boot, before the panel serves.
	connGuardWanted atomic.Bool

	// webhookCh is the outbound-webhook delivery queue drained by a small worker
	// pool (see webhooks.go). Buffered so an event emit never blocks the caller;
	// a full queue drops the delivery with a log rather than stalling the panel.
	webhookCh chan webhookJob

	operaDir string            // dir holding the opera-proxy helper binary
	operaSup *opera.Supervisor // runs/restarts the opera-proxy helper

	health laneHealth // liveness of the Opera lane (probed in healthLoop)
}

// New builds a Manager. opts carries non-DB generation parameters (e.g. the
// panel's loopback fallback dest); tls carries the managed cert paths; operaDir
// is where the opera-proxy helper binary is downloaded/run from.
func New(st *store.Store, sup *xray.Supervisor, opts xray.Options, tls TLSPaths, operaDir string) *Manager {
	m := &Manager{
		store:       st,
		sup:         sup,
		opts:        opts,
		tls:         tls,
		reconcileCh: make(chan struct{}, 1),
		accLast:     make(map[string]int64),
		applied:     make(map[int64]struct{}),
		tz:          time.Local,
		guard:       newBruteGuard(),
		operaDir:    operaDir,
		operaSup:    opera.New(filepath.Join(operaDir, "opera-proxy")),
		webhookCh:   make(chan webhookJob, webhookQueueSize),
	}
	if set, err := st.GetSettings(); err == nil {
		m.tz = loadLocation(set.Timezone)
		logbuf.SetLocation(m.tz)                             // stamp log lines in the operator's zone, not the server's
		m.proxies = proxypool.Parse(set.Routing.ProxyManual) // manual seed (instant)
		if set.OperaEnabled {
			// Bring the helper up in the background so a cold-cache download can't
			// stall startup; the "opera" lane falls back to direct until it's ready.
			go func() { _ = m.syncOpera(true, set.OperaCountryOr(), set.OperaPortOr()) }()
		}
	}
	m.sup.SetOnCrash(m.onXrayCrash) // alert admins when Xray exits unexpectedly
	go m.reconcileLoop()
	go m.proxyLoop()
	go m.bruteGuardLoop()
	go m.healthLoop()              // probe Opera/Hola lane liveness for the UI
	m.startWebhookWorkers()        // drain the outbound-webhook delivery queue
	go m.prewarmRoutingTemplates() // warm the routing-template cache so the first
	//                                  Happ/INCY sub pull after a restart doesn't block
	// NOTE: the initial proxy-pool load is done synchronously by main.go via
	// SeedProxies() before the first reconcile, so Xray starts once (with proxies)
	// rather than starting empty and restarting when a background fetch lands.
	return m
}

// loadLocation resolves an IANA timezone name, falling back to server-local time
// for an empty or unknown zone.
func loadLocation(name string) *time.Location {
	if name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		logWarn("timezone not found, using server local time", "timezone", name, "err", err)
		return time.Local
	}
	return loc
}

// loc returns the operator's configured timezone (defaults to server-local).
func (m *Manager) loc() *time.Location {
	m.tzMu.RLock()
	defer m.tzMu.RUnlock()
	if m.tz == nil {
		return time.Local
	}
	return m.tz
}

// Location exposes the operator timezone for handlers that compute date ranges.
func (m *Manager) Location() *time.Location { return m.loc() }

// RecordAccess notes a connection from an Xray access-log line (email "uN" +
// source IP). Throttled to one DB write per user+IP per 10s to absorb bursts.
func (m *Manager) RecordAccess(email, ip string) {
	if !strings.HasPrefix(email, "u") {
		return
	}
	id, err := strconv.ParseInt(email[1:], 10, 64)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	key := email + "|" + ip
	m.accMu.Lock()
	if now-m.accLast[key] < 10 {
		m.accMu.Unlock()
		return
	}
	m.accLast[key] = now
	if len(m.accLast) > accLastMax {
		for k, ts := range m.accLast { // drop pairs not seen within the TTL
			if now-ts > accLastTTL {
				delete(m.accLast, k)
			}
		}
	}
	m.accMu.Unlock()
	if err := m.store.AddConnection(id, ip, now); err != nil {
		return
	}
	// A new device (source IP) may push the user over their device cap — re-check
	// the working set and sync promptly so the over-limit user drops out, instead
	// of waiting for the next periodic reconcile.
	if working, err := m.store.WorkingUsers(now); err == nil && m.workingChanged(working) {
		m.TriggerUserSync()
	}
}

// TriggerReconcile requests a FULL config reload (regenerate + restart Xray) for
// structural changes (protocols, routing, DNS, WARP, TLS, ports). Non-blocking;
// the reload happens shortly after so the triggering HTTP response flushes first.
func (m *Manager) TriggerReconcile() {
	m.structuralPending.Store(true)
	m.signalReload()
}

// TriggerUserSync requests a live user-set sync (add/remove users via the Xray
// API, no restart) for user-only changes — far cheaper than a full reload.
func (m *Manager) TriggerUserSync() {
	m.signalReload()
}

func (m *Manager) signalReload() {
	select {
	case m.reconcileCh <- struct{}{}:
	default: // a reload is already queued
	}
}

func (m *Manager) reconcileLoop() {
	for range m.reconcileCh {
		time.Sleep(reconcileDebounce) // let the response flush + coalesce bursts
		drain(m.reconcileCh)
		// A structural change queued in this window upgrades the batch to a full
		// reload; otherwise a live user-sync suffices.
		if m.structuralPending.Swap(false) {
			m.reconcileOnce()
		} else {
			m.syncUsersOnce()
		}
	}
}

// syncUsersOnce runs one live user-sync, falling back to a full reconcile on any
// error so Xray never drifts from the DB.
func (m *Manager) syncUsersOnce() {
	defer func() {
		if r := recover(); r != nil {
			logErr("user sync: panic recovered", "panic", r)
		}
	}()
	if err := m.syncUsers(); err != nil {
		logWarn("user sync failed, falling back to full reconcile", "err", err)
		m.reconcileOnce()
	}
}

// syncUsers brings the running Xray's inbound users in line with the current
// working set using the live add/remove-user API (no restart), then rewrites
// config.json so a crash-restart preserves the change.
func (m *Manager) syncUsers() error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if !m.sup.Running() {
		return m.reconcileLocked() // can't live-update a stopped Xray
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	users, err := m.store.WorkingUsers(time.Now().Unix())
	if err != nil {
		return err
	}

	working := make(map[int64]model.User, len(users))
	for _, u := range users {
		working[u.ID] = u
	}

	m.appliedMu.Lock()
	var added []model.User
	var removedEmails []string
	for id := range m.applied {
		if _, ok := working[id]; !ok {
			removedEmails = append(removedEmails, model.UserEmail(id))
		}
	}
	for id, u := range working {
		if _, ok := m.applied[id]; !ok {
			added = append(added, u)
		}
	}
	m.appliedMu.Unlock()

	if len(added) == 0 && len(removedEmails) == 0 {
		return nil
	}
	logInfo("user sync (live)", "added", len(added), "removed", len(removedEmails))

	apiAddr := m.sup.APIAddr()
	if len(removedEmails) > 0 {
		if err := m.sup.RemoveUsers(apiAddr, xray.EnabledInboundTags(set), removedEmails); err != nil {
			return err
		}
	}
	if len(added) > 0 {
		if err := m.sup.AddUsers(apiAddr, xray.UserInbounds(set, added)); err != nil {
			return err
		}
	}
	// Keep config.json current (no restart) so the monitor's crash-restart loads
	// the right user set.
	cfg, err := xray.Generate(set, users, m.opts, m.getProxies())
	if err != nil {
		return err
	}
	if err := m.sup.WriteConfig(cfg); err != nil {
		return err
	}
	m.setApplied(users)
	// Xray's HandlerService can't live-apply user changes to the Hysteria2 (QUIC)
	// inbound — its authenticator is fixed when the inbound starts. The live adu/rmu
	// above already made VLESS/Trojan/Reality reflect the change instantly; only
	// Hysteria still needs a restart to pick it up. Defer that through the normal
	// reconcile path instead of restarting inline: the reconcile debounce coalesces a
	// burst of user changes into a SINGLE restart, and live traffic isn't dropped on
	// every add when nothing but Hysteria membership needs the reload. Trade-off: a
	// removed/disabled user keeps Hysteria access until this restart (~1 debounce
	// cycle later) — acceptable, and VLESS/Trojan access was already revoked live.
	if set.HysteriaEnabled {
		m.TriggerReconcile()
	}
	return m.store.MarkConfigApplied()
}

// reconcileOnce runs one reconcile, recovering from panics so a single bad
// config (or store error) can't kill the loop and silently freeze all future
// config updates.
func (m *Manager) reconcileOnce() {
	defer func() {
		if r := recover(); r != nil {
			logErr("reconcile: panic recovered", "panic", r)
		}
	}()
	if err := m.Reconcile(); err != nil {
		logErr("reconcile failed", "err", err)
	}
}

func drain(ch chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// Store exposes the underlying store for read-only handlers.
func (m *Manager) Store() *store.Store { return m.store }

// Reconcile regenerates the Xray config from current DB state and applies it.
// Failures are recorded in settings.last_config_error and returned. It serializes
// with the live user-sync via applyMu.
func (m *Manager) Reconcile() error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	return m.reconcileLocked()
}

// reconcileLocked is the body of Reconcile; the caller must hold applyMu.
func (m *Manager) reconcileLocked() error {
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	users, err := m.store.WorkingUsers(time.Now().Unix())
	if err != nil {
		return err
	}
	cfg, err := xray.Generate(set, users, m.opts, m.getProxies())
	if err != nil {
		logErr("reconcile: config generation failed", "err", err)
		_ = m.store.SetConfigError(err.Error())
		return err
	}
	if err := m.sup.Apply(cfg); err != nil {
		logErr("reconcile: applying config failed", "err", err)
		_ = m.store.SetConfigError(err.Error())
		return err
	}
	m.setApplied(users)
	logInfo("reconcile: config applied", "users", len(users))
	return m.store.MarkConfigApplied()
}

// setApplied records which user IDs are in the freshly-applied config.
func (m *Manager) setApplied(users []model.User) {
	ids := make(map[int64]struct{}, len(users))
	for _, u := range users {
		ids[u.ID] = struct{}{}
	}
	m.appliedMu.Lock()
	m.applied = ids
	m.appliedMu.Unlock()
}

// workingChanged reports whether the given working set differs from what's
// currently applied (someone crossed a limit/expiry, or was reset/extended).
func (m *Manager) workingChanged(users []model.User) bool {
	m.appliedMu.Lock()
	defer m.appliedMu.Unlock()
	if len(users) != len(m.applied) {
		return true
	}
	for _, u := range users {
		if _, ok := m.applied[u.ID]; !ok {
			return true
		}
	}
	return false
}
