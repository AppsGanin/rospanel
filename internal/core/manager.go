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

	"github.com/AppsGanin/rospanel/internal/abuse"
	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/connguard"
	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/ipblock"
	"github.com/AppsGanin/rospanel/internal/logbuf"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/opera"
	"github.com/AppsGanin/rospanel/internal/shaper"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/sysstat"
	"github.com/AppsGanin/rospanel/internal/turnrelay"
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

// accLast exists for one thing: collapsing a user+IP to one recorded sighting per
// accThrottle seconds. An entry older than that throttles nothing, so once the map
// grows past accLastMax it is swept of exactly those — and swept at most once per
// throttle window. It used to keep entries for an hour and sweep on EVERY sighting
// while over the cap: past ~4096 pairs active within the hour (a couple of thousand
// users on phones) every sighting walked the whole map under the access-log reader's
// lock, and at 20,000 pairs that was ~0.1ms per sighting, thousands of times a second.
//
// accThrottle only holds back a pair already recorded: a new address is recorded on
// its first line, so a new device counts at once. For a pair that stays connected it
// decides how fresh last_seen is, and that has to stay inside the two-minute online
// window (model.DeviceOnlineWindow) or a connected device drops out of the count. At
// 45s a pair seen on every node sync (13–27s apart) is written every 45–72s, and one
// seen by the master's minute-long tunnel poll on every poll. It was 10s, which wrote
// every sync's worth of sightings again: with 50,000 active users that was the
// largest share of the panel's CPU spent on writes.
const (
	accThrottle = int64(45)
	accLastMax  = 4096
	accLastTTL  = accThrottle
	// accFlushAt is how many buffered sightings bring the next flush forward instead of
	// waiting out the interval. accPendingMax bounds the buffer for the case where
	// flushes keep failing and it stops draining.
	//
	// The cap used to be 8,192 and nothing flushed early. A node's sync hands over up
	// to that many samples at once, so two busy nodes landing in one flush interval
	// overflowed it, and every sighting past the cap was dropped — device counts and
	// online status for whoever came last.
	accFlushAt    = 8192
	accPendingMax = 1 << 17
)

// Manager is the application service layer.
type Manager struct {
	store       *store.Store
	sup         *xray.Supervisor
	opts        xray.Options
	tls         TLSPaths
	certFacts   certFacts // what the certificate file said, while it is the same file
	reconcileCh chan struct{}
	// done is closed by Close and is what every background loop watches. wg counts
	// those loops so Close can WAIT for them rather than just asking them to stop:
	// the whole point is that when Close returns, nothing is left that could still
	// touch the store — a goroutine that outlives the database it writes to shows up
	// as "sql: database is closed" long after the code that caused it has moved on.
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	// structuralPending marks the next queued reload as a full restart (config
	// changed), vs a cheap live user-sync. Set by TriggerReconcile.
	structuralPending atomic.Bool

	accMu   sync.Mutex
	accLast map[string]int64 // throttle key "uN|ip" → last recorded unix
	// accFlushDue asks the access flush loop to flush now (see accFlushAt).
	accFlushDue chan struct{}
	// accLastSwept is when accLast was last swept (unix), so a map that stays over its
	// cap with genuinely active pairs is not re-walked on every sighting.
	accLastSwept int64
	// deviceCheckedAt is when a flush last re-checked the device limits (unix; see
	// deviceCheckEvery).
	deviceCheckedAt atomic.Int64
	// accPending buffers sightings between flushes, so the access-log reader never
	// touches the database on the hot path. Bounded by the throttle above: one entry
	// per user+IP per flush interval, not per log line.
	accPending map[accPendingKey]store.ConnectionHit

	// abuse holds the blocklists and their refresh loop. Nil when the feature is off,
	// which the hot path treats as "matches nothing".
	abuse *abuse.Store
	// abuseMu guards the match buffer and the alert dedupe. Separate from accMu so a
	// match never contends with the connections path it rides along with.
	abuseMu      sync.Mutex
	abusePending map[abusePendingKey]store.AbuseHit
	abuseAlerted map[abuseAlertKey]struct{} // (user, day) already alerted for
	abuseDropAt  int64                      // unix secs of the last "buffer full" log

	// userIDCache is a short-lived snapshot of existing user ids, used to validate
	// node-reported sites without a full id scan on the single connection per sync.
	// The map is shared read-only with callers — never mutate it in place.
	userIDCacheMu sync.Mutex
	userIDCache   map[int64]struct{}
	userIDCacheAt time.Time

	// applyMu serializes config application (Reconcile + the live user-sync) so a
	// direct Reconcile (e.g. from tlsLoop on cert renewal) can't interleave with the
	// reconcile-loop's user-sync and leave config.json / the applied set divergent.
	applyMu sync.Mutex

	appliedMu sync.Mutex
	applied   map[int64]struct{} // user IDs currently in the applied config

	// enforceMu runs one traffic enforcement pass at a time, and enforcePending marks a
	// pass already scheduled for node reports to share (see enforceTrafficSoon).
	enforceMu      sync.Mutex
	enforcePending atomic.Bool

	// stateGate keeps node states from being built and encoded side by side.
	stateGate stateGate
	// nodeInputsMu guards nodeInputsCache, the fleet-wide inputs every node's desired
	// state is built from (see nodeInputs).
	nodeInputsMu      sync.Mutex
	nodeInputsCache   *nodeInputs
	nodeInputsVersion uint64
	// nodeInputsLast is the last complete read, kept when the cache is dropped: each new
	// version is compared with it and what differs goes in nodeJournal (see
	// manager_node_split.go).
	nodeInputsLast *nodeInputs
	nodeJournal    inputsJournal

	// summaryMu guards the user counts SystemStatus reuses (see recentSummary).
	summaryMu    sync.Mutex
	summaryCache *Summary
	summaryAt    time.Time

	// nodeStateMu guards nodeStates, each node's last built state fingerprint and hash
	// (see NodeStateChange).
	nodeStateMu sync.Mutex
	nodeStates  map[int64]nodeStateMemo
	nodeSplits  map[int64]nodeSplitMemo
	// boot tells this process's split-state tags from an earlier one's (see bootID).
	boot     string
	bootOnce sync.Once
	// served is who each node's last built state lets in: what its reports are
	// believed about (see manager_node_served.go).
	served servedRegistry

	tzMu sync.RWMutex
	tz   *time.Location // operator timezone for the local-day stats boundary

	sys *sysstat.Sampler // host metrics sampler for the dashboard (nil until started)

	tmplMu    sync.Mutex
	tmplCache map[string]routingTmpl // cached routing templates by URL

	tgSDKMu     sync.Mutex
	tgSDKBody   []byte        // cached telegram.org telegram-web-app.js (nil until first fetch)
	tgSDKAt     time.Time     // when tgSDKBody was fetched
	tgSDKFailAt time.Time     // when the last fetch failed; suppresses inline retries for a cooldown
	tgSDKLogAt  time.Time     // when the last fetch failure was logged; rate-limits that line
	tgSDKWait   chan struct{} // non-nil while a fetch is in flight; closed when it lands (singleflight)

	// userNotify pushes a message to a VPN user's Telegram chat (set by the user
	// bot; nil when off); adminNotify broadcasts to the admin chats (set by the
	// admin bot). Used e.g. to report payment start/completion. adminModerate asks
	// the admin bot to post a signup awaiting moderation with approve/reject buttons.
	notifyMu      sync.Mutex
	userNotify    func(chatID int64, html string)
	adminNotify   func(html string)
	adminModerate func(reqID int64, name, plan string)
	adminLogin    func(LoginAlert) // a sign-in from a new address, with the revoke button

	// notifyThrottle bounds the rate of repeatable system alerts (Xray crash loop,
	// cert renewal errors) so a stuck condition can't flood the admin chats.
	throttleMu      sync.Mutex
	lastCrashNotify time.Time
	// crashAlerted records that admins were actually told about the current outage,
	// so the all-clear is only sent for an alarm they saw.
	crashAlerted      bool
	lastCertErrNotify time.Time

	// certMu serializes certificate writes. tlsLoop retries while no CA cert exists and
	// the operator can issue one from the panel at the same moment; the two share fixed
	// staging filenames and rename cert and key separately, so an overlap can leave one
	// issuance's certificate beside another's key — unserveable, and invisible to every
	// health check. See Manager.ensureCert.
	certMu sync.Mutex

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

	geoMu     sync.Mutex
	geoSite   []string     // cached geosite category codes
	geoIP     []string     // cached geoip category codes
	geoGroups geo.GroupSet // cached iplist groups ("<source>/<group>" → rules)
	geoGen    uint64       // counts changes of geoGroups, so a node state knows its groups are current

	// countryLookup resolves connection IPs to countries for the geo breakdown, built
	// lazily from geoip.dat and rebuilt when the file changes (a geo refresh).
	geoLookupMu  sync.Mutex
	geoLookup    *geo.CountryLookup
	geoLookupMod time.Time
	// asnTable mirrors geoLookup for the IP→ASN (provider) table.
	asnLookupMu sync.Mutex
	asnTable    *geo.ASNLookup
	asnTableMod time.Time

	proxyMu sync.Mutex
	// proxies holds the local server's current egress proxies of each lane, keyed by
	// lane ID.
	proxies map[string][]model.ProxyEndpoint
	// nodeProxies holds each remote node's own resolved lane proxies, keyed by node
	// ID then lane ID. A node egresses through its OWN proxy pool (independent of the
	// master), so its lanes are resolved separately. Refreshed on the same cadence.
	nodeProxies map[int64]map[string][]model.ProxyEndpoint

	// proxyListMu guards proxyLists: the lines each proxy-list URL returned on its
	// last successful fetch, which a failed fetch falls back to. Its own lock, not
	// proxyMu, because buildProxies runs while callers are about to take proxyMu.
	proxyListMu sync.Mutex
	proxyLists  map[string][]string

	guard *bruteGuard

	// shaper installs the per-user speed caps on this machine; wan is the interface
	// it acts on, resolved once (see manager_shaper.go).
	shaper *shaper.Applier
	wanMu  sync.Mutex
	wan    string

	// devNotice keeps a refused device quiet after its first report — a client that
	// hit the device cap retries on its own schedule and would otherwise alert the
	// operator on every retry (see manager_devices.go).
	devNotice *deviceNotice
	// payNotice keeps a stuck payment quiet after its first report. The provider poll
	// re-reads an unresolved order every 25s, so an alert with no throttle is thousands
	// of identical Telegram messages a day for one order.
	payNotice *deviceNotice
	// siteNotice keeps a node that sends more destination rows than the panel takes
	// from logging it on every sync.
	siteNotice *deviceNotice

	// connGuardWanted records whether the operator asked for the per-IP connection
	// guard (ROSPANEL_CONNLIMIT != off). Needed to tell "off on purpose" apart from
	// "on, but nftables silently refused it" in the health report — the second is a
	// problem, the first isn't. Set once at boot, before the panel serves.
	connGuardWanted atomic.Bool
	// connGuardLimits are the tunables the guard is (re-)applied with. Held so the
	// port set can be refreshed at runtime — a custom inbound added after boot must
	// come under the same guard as the built-in lanes.
	connGuardMu     sync.Mutex
	connGuardLimits connguard.Limits

	// webhookCh is the outbound-webhook delivery queue drained by a small worker
	// pool (see webhooks.go). Buffered so an event emit never blocks the caller;
	// a full queue drops the delivery with a log rather than stalling the panel.
	webhookCh chan webhookJob

	operaDir string            // dir holding the opera-proxy helper binary
	operaSup *opera.Supervisor // runs/restarts the opera-proxy helper

	health laneHealth // liveness of the Opera lane (probed in healthLoop)

	// nodes tracks per-node wake channels so a config change wakes any held node
	// long-poll to re-pull desired state (see manager_nodes.go).
	nodes *nodeRegistry
	// probes tracks in-flight "is this port free on that node" questions, answered
	// over the same long-poll (see manager_node_probe.go); checks does the same for
	// "does your Xray accept this config" (see manager_node_check.go).
	probes *probeRegistry
	checks *checkRegistry
	// nodePathCB live-swaps the node-API URL segment into the router when the first
	// node is created (nil until the server registers it; nil-safe for CLI/tests).
	nodePathMu sync.Mutex
	nodePathCB func(string)
	// nodeEnsureMu serializes first-time node-API path generation so concurrent
	// node creates converge on one segment.
	nodeEnsureMu sync.Mutex
	// nodeUpdateMu serialises the read-then-mark handover of a one-shot node command
	// (self-update, geo refresh). The commands themselves live in node_commands on
	// disk — see migration 0054 and Manager.takeCmd — so a panel restart no longer
	// drops what an operator asked for.
	nodeUpdateMu sync.Mutex
	// nodeRestart holds Xray-restart requests that have not been confirmed yet. Unlike
	// the two flags above, a restart is not done when it is sent: the operator needs to
	// know it actually happened, so the request outlives its delivery and is only
	// dropped when the node reports a bounced Xray (or the wait times out).
	nodeRestart map[int64]*nodeRestartReq

	// nodeHostStats is each node's last-reported machine state (disk/RAM/guards) for
	// its diagnostics page, under nodeGeoMu with the other "last reported" caches.
	// Bounded by the node count; a deleted node's entry is dead weight of one struct.
	nodeHostStats map[int64]nodeapi.HostStats
	// nodeAWG is each node's last-reported AmneziaWG state. Absent until a node
	// reports one, which is how an agent older than the feature is told apart from a
	// tunnel that is genuinely down — the difference between "nothing known" and "it
	// is broken", and alerting on the first would page every operator mid-upgrade.
	nodeAWG map[int64]nodeAWGState
	// online is who is connected to which server right now (see manager_online.go).
	online onlineGauge

	// probeBlock drops scanners at the firewall, policyBlock the addresses the source
	// policy refuses (manager_connpolicy.go). Separate tables: switching one off must
	// not lift the other's blocks. Nil in a test manager, where both are no-ops.
	probeBlock  *ipblock.Blocker
	policyBlock *ipblock.Blocker
	// ipBan drops the addresses an operator banned by hand (manager_ipban.go): its own
	// permanent table, since a ban lasts until it is lifted. banMu serializes placing,
	// lifting and re-applying bans, so the table and the kernel cannot cross over; hosts
	// resolves the servers' host names a ban must not cover (nil: none are resolved).
	ipBan *ipblock.Blocker
	banMu sync.Mutex
	hosts *hostAddrs
	// policy caches the source policy and the addresses it has recently ruled on
	// (manager_connpolicy.go); the check runs on the connection path.
	policy policyState
	// trusted caches the networks no automatic ban may touch (manager_trusted.go).
	trusted trustedState

	// awg is the master's AmneziaWG tunnel (see manager_awg.go); awgLast holds the
	// counters read at the previous poll, per peer public key.
	awg     awg.Device
	awgMu   sync.Mutex
	awgLast map[string]awg.PeerStat

	// turn runs the master's TURN relays, one per WireGuard inbound (manager_wireguard.go).
	turn *turnrelay.Relay

	// nodeLogs holds the most recent log tail reported by each node, plus which
	// nodes an operator is currently viewing (so the panel asks them for logs).
	nodeLogsMu     sync.Mutex
	nodeLogs       map[int64]nodeLogEntry
	nodeLogsWanted map[int64]int64 // node id → unix time the operator last asked

	// nodeGeoFiles holds each node's last-reported geo database status.
	nodeGeoMu    sync.Mutex
	nodeGeoFiles map[int64][]nodeapi.GeoFile
	// nodeSyncFails holds each node's last-reported count of sync failures in the past
	// hour — the "limping transport" signal that a still-online node is degraded.
	nodeSyncFails map[int64]int
	// nodeHas is what each node last said it holds — the parts revision it speaks and the
	// tag of its state — so the health report can ask the question a sync would.
	nodeHas map[int64]NodeHas

	// nodeAlerts is what admins were last told about each node's reachability, Xray
	// and certificate — the fleet-wide half of the "Xray failure" / "TLS certificate"
	// admin events (see manager_nodes_notify.go).
	nodeAlertMu sync.Mutex
	// nodeTraffic caches each server's usage in its cap period; the node watch loop
	// refreshes it and the subscription path reads it (see manager_node_traffic.go).
	nodeTraffic nodeTrafficCache
	nodeAlerts  map[int64]*nodeAlertState
}

// nodeLogEntry is a node's last-reported log tail.
type nodeLogEntry struct {
	lines []string
	at    int64
}

// New builds a Manager. opts carries non-DB generation parameters (e.g. the
// panel's loopback fallback dest); tls carries the managed cert paths; operaDir
// is where the opera-proxy helper binary is downloaded/run from.
func New(st *store.Store, sup *xray.Supervisor, opts xray.Options, tls TLSPaths, operaDir string) *Manager {
	m := &Manager{
		store:          st,
		sup:            sup,
		opts:           opts,
		tls:            tls,
		reconcileCh:    make(chan struct{}, 1),
		done:           make(chan struct{}),
		accLast:        make(map[string]int64),
		accPending:     make(map[accPendingKey]store.ConnectionHit),
		accFlushDue:    make(chan struct{}, 1),
		abusePending:   make(map[abusePendingKey]store.AbuseHit),
		abuseAlerted:   make(map[abuseAlertKey]struct{}),
		applied:        make(map[int64]struct{}),
		tz:             time.Local,
		guard:          newBruteGuard(),
		shaper:         shaper.New(),
		devNotice:      newDeviceNotice(),
		payNotice:      newNotice(6 * time.Hour),
		siteNotice:     newNotice(time.Hour),
		operaDir:       operaDir,
		operaSup:       opera.New(filepath.Join(operaDir, "opera-proxy")),
		webhookCh:      make(chan webhookJob, webhookQueueSize),
		nodes:          newNodeRegistry(),
		probes:         newProbeRegistry(),
		checks:         newCheckRegistry(),
		nodeRestart:    map[int64]*nodeRestartReq{},
		nodeLogs:       map[int64]nodeLogEntry{},
		nodeGeoFiles:   map[int64][]nodeapi.GeoFile{},
		nodeHostStats:  map[int64]nodeapi.HostStats{},
		nodeAWG:        map[int64]nodeAWGState{},
		awg:            awg.New(),
		turn:           turnrelay.New(),
		probeBlock:     ipblock.New(ipblock.TableProbes),
		policyBlock:    ipblock.New(ipblock.TablePolicy),
		ipBan:          ipblock.NewPermanent(ipblock.TableBanned),
		hosts:          newHostAddrs(nil),
		nodeSyncFails:  map[int64]int{},
		nodeLogsWanted: map[int64]int64{},
		nodeAlerts:     map[int64]*nodeAlertState{},
	}
	if set, err := st.GetSettings(); err == nil {
		m.tz = loadLocation(set.Timezone)
		logbuf.SetLocation(m.tz)                       // stamp log lines in the operator's zone, not the server's
		m.proxies = seedProxiesFromManual(set.Routing) // manual seed (instant)
		m.seedNodeProxies()                            // per-node manual seed (instant)
		// Resolve each node's URL-sourced lane proxies in the background (mirrors the
		// master's SeedProxies, which service.go runs unconditionally at boot). Without
		// this, a node's URL lanes would stay empty until the first proxyLoop tick — and
		// forever when auto-refresh is "never", since the loop is cadence-gated.
		m.runAsync(m.RefreshNodeProxies)
		if set.OperaEnabled {
			// Bring the helper up in the background so a cold-cache download can't
			// stall startup; the "opera" lane falls back to direct until it's ready.
			m.runAsync(func() { _ = m.syncOpera(true, set.OperaCountryOr(), set.OperaPortOr()) })
		}
	}
	m.sup.SetOnCrash(m.onXrayCrash)             // alert admins when Xray exits unexpectedly
	m.sup.SetOnRecover(m.onXrayRecover)         // ...and tell them when it is back
	m.sup.SetOnRolledBack(m.onConfigRolledBack) // ...and when a change was undone to get it back
	m.sup.SetOnWedged(m.onXrayWedged)           // ...and when the watchdog revives a hung one
	if wd, err := st.GetSettings(); err == nil {
		m.sup.SetWatchdogEnabled(wd.WatchdogEnabled) // honour the operator's toggle
	}
	m.sup.StartWatchdog() // auto-restart a wedged (alive-but-not-serving) Xray
	// The same two alerts for the remote nodes. They have no bot of their own, and a
	// node that stops syncing altogether can only be noticed on a timer.
	// Every one of these goes through runAsync, so Close can wait for it. A loop that
	// is started with a bare `go` is one Close cannot account for, and the failure is
	// invisible until something it writes to has already been torn down.
	m.runAsync(func() { m.guard.cleanupLoop(m.done) })
	m.runAsync(m.nodeWatchLoop)
	m.runAsync(m.reconcileLoop)
	m.runAsync(m.proxyLoop)
	m.runAsync(m.geoLoop)         // auto-refresh geo databases on the operator's cadence
	m.runAsync(m.ipListLoop)      // ...and the iplist lists on their own, separate cadence
	m.runAsync(m.probeDigestLoop) // once-a-day summary of new secret-path scanners (opt-in)
	m.runAsync(m.bruteGuardLoop)
	m.runAsync(m.shaperLoop)              // per-user speed caps follow the addresses users connect from
	m.runAsync(m.healthLoop)              // probe Opera/Hola lane liveness for the UI
	m.startWebhookWorkers()               // drain the outbound-webhook delivery queue
	m.runAsync(m.prewarmRoutingTemplates) // warm the routing-template cache so the first
	//                                  Happ/INCY sub pull after a restart doesn't block
	// NOTE: telegram-web-app.js is deliberately NOT prewarmed here. The cold path in
	// TelegramWebAppSDK fetches it inline and serves it, so a warm-up would only save
	// the first subscription-page view ~120ms — not worth an unconditional outbound
	// call to telegram.org on every single start (a beacon the decoy story doesn't
	// cover, and one that made `go test ./internal/server` hit the real network,
	// since its tests build a Manager through New).
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

// accPendingKey identifies one buffered sighting.
type accPendingKey struct {
	userID int64
	ip     string
}

// RecordAccess notes a connection from an Xray access-log line (email "uN" +
// source IP, and the destination host when the line carried a usable one).
// Throttled to one recorded sighting per user+IP per accThrottle, then buffered —
// FlushAccess writes them.
//
// This is called from the access-log reader for every line Xray emits, so it does
// no I/O at all: it takes a lock, updates two maps, and returns.
func (m *Manager) RecordAccess(email, ip, dest string) {
	if !strings.HasPrefix(email, "u") {
		return
	}
	id, err := strconv.ParseInt(email[1:], 10, 64)
	if err != nil {
		return
	}
	// Abuse matching runs BEFORE the throttle, deliberately: the throttle below
	// collapses a user+IP to one sighting per accThrottle (right for counting devices), and a
	// low-volume malware callback is exactly the traffic that gate would hide. Matched
	// on the FULL destination — feeds list specific hosts, and a listed subdomain of an
	// unlisted parent must not be missed. Memory-only lookup, so it costs the hot path
	// a hash probe rather than a write.
	m.recordAbuse(id, dest)

	now := time.Now().Unix()
	key := email + "|" + ip
	m.accMu.Lock()
	defer m.accMu.Unlock()
	if now-m.accLast[key] < accThrottle {
		return
	}
	pk := accPendingKey{userID: id, ip: ip}
	h, buffered := m.accPending[pk]
	// Bound the buffer. It normally drains every few seconds, but a persistent write
	// failure (a full disk, say) makes FlushAccess requeue instead — and the throttle
	// above stops protecting us as soon as accLast evicts a key, since that reopens
	// the pair for buffering. Dropping the newest sighting for a pair we are not
	// already tracking costs a last_seen update; growing without limit costs the
	// process. Checked before the throttle is armed: a sighting turned away here is
	// taken again on the pair's next line rather than throttled as if it were kept.
	if !buffered && len(m.accPending) >= accPendingMax {
		return
	}
	m.accLast[key] = now
	if len(m.accLast) > accLastMax && now-m.accLastSwept >= accThrottle {
		m.accLastSwept = now
		for k, ts := range m.accLast { // drop pairs not seen within the TTL
			if now-ts > accLastTTL {
				delete(m.accLast, k)
			}
		}
	}
	h.UserID, h.IP, h.Hits = id, ip, h.Hits+1
	if now > h.SeenAt {
		h.SeenAt = now
	}
	m.accPending[pk] = h
	if len(m.accPending) >= accFlushAt {
		select {
		case m.accFlushDue <- struct{}{}:
		default: // already asked, or no loop to ask (tests)
		}
	}
}

// AccessFlushDue fires when enough sightings are buffered that the access flush loop
// should not wait for its next tick.
func (m *Manager) AccessFlushDue() <-chan struct{} { return m.accFlushDue }

// FlushAccess writes the buffered access sightings in one transaction and, if the
// new devices changed who should be online, syncs Xray.
//
// The device-cap re-check used to run per sighting — a full WorkingUsers query for
// every user+IP every 10s. It belongs here: it only has to happen when something
// was actually recorded, and once per batch answers the same question.
func (m *Manager) FlushAccess() {
	m.accMu.Lock()
	if len(m.accPending) == 0 {
		m.accMu.Unlock()
		return
	}
	hits := make([]store.ConnectionHit, 0, len(m.accPending))
	for _, h := range m.accPending {
		hits = append(hits, h)
	}
	clear(m.accPending)
	m.accMu.Unlock()

	started, err := m.store.RecordConnections(hits)
	if err != nil {
		// Put them back rather than drop them: the buffer was already drained, so
		// returning here would silently lose the sightings, and stale last_seen /
		// undercounted devices feed straight into the device cap. Merging (rather than
		// overwriting) keeps whatever arrived while the write was in flight.
		m.accMu.Lock()
		for _, h := range hits {
			pk := accPendingKey{userID: h.UserID, ip: h.IP}
			cur, buffered := m.accPending[pk]
			if !buffered && len(m.accPending) >= accPendingMax {
				continue // same bound as RecordAccess: shed rather than grow forever
			}
			cur.UserID, cur.IP = h.UserID, h.IP
			cur.Hits += h.Hits
			if h.SeenAt > cur.SeenAt {
				cur.SeenAt = h.SeenAt
			}
			m.accPending[pk] = cur
		}
		m.accMu.Unlock()
		logErr("access: flush failed, sightings requeued", "sightings", len(hits), "err", err)
		return
	}
	m.noteTermsStarted(started)
	now := time.Now().Unix()
	// The device check below is two passes over everyone online, and the flush runs
	// every few seconds; with 50,000 active users that was a third of what the flush
	// cost, repeating an answer that cannot change faster than the grace allows.
	if last := m.deviceCheckedAt.Load(); now-last < deviceCheckEvery {
		return
	}
	m.deviceCheckedAt.Store(now)
	// Stamp who is over their device limit before asking who should be in the config:
	// the cut waits out model.DeviceLimitGrace, and the grace measures from this stamp.
	// Sightings have just landed, so this is the moment the answer can change.
	if err := m.store.StampDeviceOverLimit(now); err != nil {
		logErr("access: device-limit stamp failed", "err", err)
	}
	// A new device (source IP) may push the user over their device cap — re-check
	// the working set and sync promptly so the over-limit user drops out, instead
	// of waiting for the next periodic reconcile.
	if working, err := m.store.WorkingUserIDs(now); err == nil && m.workingIDsChanged(working) {
		m.TriggerUserSync()
	}
}

// deviceCheckEvery is how often a flush re-checks who is over their device limit.
// A user is cut only once DeviceLimitGrace (150s) has passed since the stamp. The stamp
// can come up to this much after the extra device appears, and the check that notices
// the grace running out up to this much after that, so the cut comes 150–210s after
// the device rather than 150–160s. (The enforcement pass after node reports reads the
// working set too, and usually notices sooner.)
const deviceCheckEvery = int64(30)

// TriggerReconcile requests a FULL config reload (regenerate + restart Xray) for
// structural changes (protocols, routing, DNS, WARP, TLS, ports). Non-blocking;
// the reload happens shortly after so the triggering HTTP response flushes first.
func (m *Manager) TriggerReconcile() {
	m.structuralPending.Store(true)
	m.signalReload()
}

// runAsync starts a background goroutine that Close will wait for. Every long-lived
// loop and every fire-and-forget task the manager owns goes through here; the ones
// that do not are exactly the ones that can still be running after the store is gone.
func (m *Manager) runAsync(fn func()) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		fn()
	}()
}

// wait blocks for d and reports whether the caller should carry on. It answers false
// the moment Close is called, which turns every "sleep then work" loop into one that
// stops promptly instead of on its own cadence — a geo refresh sleeps an hour, and
// waiting an hour to shut down is indistinguishable from a hang.
func (m *Manager) wait(d time.Duration) bool {
	if d <= 0 {
		return !m.stopped()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-m.done:
		return false
	}
}

// stopped reports whether Close has been called, for the loops that check between
// steps rather than at a wait.
func (m *Manager) stopped() bool {
	select {
	case <-m.done:
		return true
	default:
		return false
	}
}

// Close stops every background goroutine the manager owns and waits for them.
//
// Waiting is the contract: after Close returns, nothing of the manager's is still
// running, so the caller can close the store without a loop writing into a closed
// database behind it. That failure is quiet and misattributed — it surfaces as
// "sql: database is closed" from whichever loop happened to tick last, in a test
// that has already reported success or a shutdown that looks clean.
//
// Idempotent, and safe to call on a manager whose loops were never started (a test
// building one directly): done is closed once and the WaitGroup is simply empty.
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		if m.done != nil {
			close(m.done)
		}
	})
	// Bounded, because not everything the manager starts is a loop that can stop on a
	// signal: a geo refresh triggered seconds before shutdown is a multi-megabyte
	// download with nowhere to check. The loops all return in microseconds, so this
	// budget is only ever spent on one of those, and spending it is better than either
	// hanging the shutdown or leaving the task unaccounted for entirely.
	stopped := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(closeGrace):
		logWarn("shutdown: background work did not finish in time; continuing",
			"grace", closeGrace)
	}
}

// closeGrace is how long Close waits for background work. Generous next to the loops
// (which stop at once) and short next to a network download, which is the only thing
// that can reach it.
const closeGrace = 5 * time.Second

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
	for {
		select {
		case <-m.done:
			return
		case <-m.reconcileCh:
		}
		// The debounce is a wait like any other: a Close during it returns instead of
		// starting a reload nobody will see the end of.
		if !m.wait(reconcileDebounce) { // let the response flush + coalesce bursts
			return
		}
		drain(m.reconcileCh)
		// A structural change queued in this window upgrades the batch to a full
		// reload; otherwise a live user-sync suffices.
		if m.structuralPending.Swap(false) {
			m.reconcileOnce()
			// The config just changed locally — wake every connected node so it
			// re-pulls its desired state (all nodes serve the same user set).
			m.notifyNodes()
			continue
		}
		// Most requests to sync are for a change no node can see: a limit raised, a
		// name or a note edited, a tariff renewed for a user who was never cut off.
		// A wake reaches every node at once and costs the panel a re-read of
		// everything the fleet is served, so the cheap half of that read is done here
		// and the nodes are left alone when it says nothing moved. Being wrong in
		// that direction only delays: a node re-reads on its next poll regardless,
		// which is half a minute at the outside.
		//
		// With nodes waiting, one read of the working set answers both questions —
		// whether the master's own config must change and whether the fleet's has —
		// where it was two scans of every user back to back. Without them only the
		// first is asked, and the ids alone are the cheaper read for it.
		var ws *workingSet
		var wsErr error
		if m.nodes.parked() > 0 {
			ws, wsErr = m.readWorkingSet()
		}
		if m.syncUsersOnce(ws, wsErr) || (ws != nil && m.fleetChanged(ws)) {
			m.notifyNodes()
		}
	}
}

// syncUsersOnce runs one live user-sync, falling back to a full reconcile on any
// error so Xray never drifts from the DB. It reports whether anything was applied —
// a panic or a fallback counts as yes, since what was applied is then unknown.
func (m *Manager) syncUsersOnce(ws *workingSet, wsErr error) (changed bool) {
	defer func() {
		if r := recover(); r != nil {
			logErr("user sync: panic recovered", "panic", r)
			changed = true
		}
	}()
	changed, err := m.syncUsers(ws, wsErr)
	if err != nil {
		logWarn("user sync failed, falling back to full reconcile", "err", err)
		m.reconcileOnce()
		return true
	}
	return changed
}

// syncUsers brings the running Xray's inbound users in line with the current
// working set using the live add/remove-user API (no restart), then rewrites
// config.json so a crash-restart preserves the change.
//
// ws is the working set the caller already read, with wsErr the error reading it;
// nil and nil when it read none, and the sync reads the ids itself.
func (m *Manager) syncUsers(ws *workingSet, wsErr error) (bool, error) {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if !m.sup.Running() {
		return true, m.reconcileLocked() // can't live-update a stopped Xray
	}
	// Who is in the config is a cheap read; what their credentials are is not, and
	// most syncs are asked for a change that moves nobody in or out. The ids settle
	// that before a single password is decrypted; the credentials below are read
	// afresh and the change derived from them, so a set that moves in between is
	// applied as it is then, not as it was here.
	if wsErr != nil {
		return false, wsErr
	}
	var ids []int64
	if ws != nil {
		ids = ws.ids
	} else {
		var err error
		if ids, err = m.store.WorkingUserIDs(time.Now().Unix()); err != nil {
			return false, err
		}
	}
	if !m.workingIDsChanged(ids) {
		return false, nil
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return false, err
	}
	users, err := m.store.WorkingCredentials(time.Now().Unix())
	if err != nil {
		return false, err
	}

	// Ids, not the users themselves: a map of fifty thousand whole users is tens of
	// megabytes of garbage for a question about who is in it.
	working := make(map[int64]struct{}, len(users))
	for i := range users {
		working[users[i].ID] = struct{}{}
	}

	m.appliedMu.Lock()
	var added []model.User
	var removedEmails []string
	for id := range m.applied {
		if _, ok := working[id]; !ok {
			removedEmails = append(removedEmails, model.UserEmail(id))
		}
	}
	for i := range users {
		if _, ok := m.applied[users[i].ID]; !ok {
			added = append(added, users[i])
		}
	}
	m.appliedMu.Unlock()

	if len(added) == 0 && len(removedEmails) == 0 {
		return false, nil
	}
	logInfo("user sync (live)", "added", len(added), "removed", len(removedEmails))

	// The custom inbounds are part of the live update, not just of the regenerated
	// config: without them a user added here would reach the built-in lanes at once
	// but a custom one only after the next full restart — and, worse, a user removed
	// here would keep working through every custom inbound until then.
	opts, err := m.genOptsFor(model.LocalNodeID)
	if err != nil {
		return true, err
	}
	custom := opts.Custom
	// A user added here may have no tunnel identity yet, and a WireGuard inbound they are
	// allowed on needs one to hold them.
	wgUsers, _, _ := m.claimWireGuard(users, custom, opts.Access)

	apiAddr := m.sup.APIAddr()
	if len(removedEmails) > 0 {
		if err := m.sup.RemoveUsers(apiAddr, xray.EnabledInboundTags(set, custom), removedEmails); err != nil {
			return true, err
		}
	}
	if len(added) > 0 {
		if err := m.sup.AddUsers(apiAddr, xray.UserInbounds(set, custom, added, model.LocalNodeID, opts.Access)); err != nil {
			return true, err
		}
	}
	// Keep config.json current (no restart) so the monitor's crash-restart loads
	// the right user set.
	cfg, err := xray.Generate(set, wgUsers, opts, m.getProxies())
	if err != nil {
		return true, err
	}
	if err := m.sup.WriteConfig(cfg); err != nil {
		return true, err
	}
	m.setApplied(users)
	// Hysteria2 users, on the built-in lane and on every custom QUIC inbound, go through
	// the supervisor: it adds and removes them without closing anyone else's connection
	// and cuts off the open connections of those removed (see xray/hysteria_live.go).
	// The list comes from the generated config by protocol, so a custom inbound is
	// never left out.
	//
	// A failure there is returned: the process then holds users no config describes, and
	// the full reload that follows restarts it.
	if err := m.sup.SyncHysteria(apiAddr, xray.HysteriaInbounds(cfg)); err != nil {
		return true, err
	}
	// WireGuard peers likewise, through the API (see xray/wireguard_live.go).
	if err := m.sup.SyncWireGuard(apiAddr, xray.WireGuardInbounds(cfg)); err != nil {
		return true, err
	}
	m.syncAWGLocked(set, users)
	return true, m.store.MarkConfigApplied()
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
	users, err := m.store.WorkingCredentials(time.Now().Unix())
	if err != nil {
		return err
	}
	opts, err := m.genOptsFor(model.LocalNodeID)
	if err != nil {
		logErr("reconcile: options load failed", "err", err)
		_ = m.store.SetConfigError(err.Error())
		return err
	}
	wgUsers, _, _ := m.claimWireGuard(users, opts.Custom, opts.Access)
	cfg, err := xray.Generate(set, wgUsers, opts, m.getProxies())
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
	m.syncTurnLocked(opts.Custom)
	m.syncAWGLocked(set, users)
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

// workingIDsChanged reports whether the given working set differs from what's
// currently applied (someone crossed a limit/expiry, or was reset/extended). It takes
// ids alone, which is all it compares: see store.WorkingUserIDs.
func (m *Manager) workingIDsChanged(ids []int64) bool {
	m.appliedMu.Lock()
	defer m.appliedMu.Unlock()
	if len(ids) != len(m.applied) {
		return true
	}
	for _, id := range ids {
		if _, ok := m.applied[id]; !ok {
			return true
		}
	}
	return false
}
