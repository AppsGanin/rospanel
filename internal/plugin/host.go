// Package plugin runs installed plugins: their lifecycle, the calls into them, and
// the host side of the `panel` API they program against. Authors' docs: docs/plugins.
//
// Every plugin is one jsvm.VM plus one pdb.DB. Calls into a plugin are serialized
// (an author never thinks about concurrency); different plugins run in parallel.
// Whatever a plugin does wrong is contained to it: its errors count towards a
// breaker that pauses it, its VM is replaced when it dies or bloats, its database
// is its own file, and the panel boots and serves whether any plugin loads or not.
package plugin

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin/jsvm"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
	"github.com/AppsGanin/rospanel/internal/plugin/pdb"
)

//go:embed prelude.js
var preludeJS string

// Store is the slice of the panel's store the host needs.
type Store interface {
	ListPlugins() ([]model.Plugin, error)
	GetPlugin(id string) (*model.Plugin, error)
	SavePlugin(model.Plugin) error
	SetPluginState(id string, enabled bool, status, statusErr string) error
	SetPluginConfig(id string, cfg map[string]string) error
	DeletePlugin(id string) error
}

// APIRequest is one panel.api call.
type APIRequest struct {
	Plugin   string
	Perms    []string // what the plugin was granted
	ReadOnly bool     // inside a decision hook: GET only
	Method   string
	Path     string
	Body     []byte
	// Sink, when set, receives the answer's body instead of the returned bytes
	// (panel.api with {blob: true}).
	Sink io.Writer
}

// APICaller serves panel.api: the /v1 handler in-process, as the plugin.
type APICaller func(ctx context.Context, req APIRequest) (status int, body []byte, err error)

// Fetcher serves panel.http.fetch for one plugin's allowlist.
type Fetcher interface {
	Fetch(ctx context.Context, allow []string, req FetchRequest) (*FetchResponse, error)
}

// Deps wire the host to the panel.
type Deps struct {
	Store        Store
	DataDir      string // plugins/<id>.db live under it; the wasm cache in cache/wasm
	PanelVersion string
	API          APICaller                                     // nil: panel.api answers "not available"
	Fetch        Fetcher                                       // nil: panel.http.fetch answers "not available"
	Notify       func(pluginID, message string, retrying bool) // tells the admins a plugin stopped
	Stopped      func(pluginID string)                         // a plugin stopped taking events: drop its queue
	Logger       *slog.Logger
	// PublicURL is the panel's public callback URL for a path under its secret
	// segment ("x/<id>", "plugin.<id>"), "" while the panel has no host or secret.
	PublicURL func(path string) string
	// NoBreaker keeps failing plugins running: the author tools, where a test makes a
	// plugin fail on purpose. Never set in a panel.
	NoBreaker bool
	// Engine, when set, is shared with another host (the panel's, for a sandbox run
	// of a draft): its compiled guest is reused and it is not closed with this host.
	Engine *jsvm.Engine
}

// Call timeouts per kind of call.
const (
	LoadTimeout   = 10 * time.Second
	EventTimeout  = 10 * time.Second
	HookTimeout   = 300 * time.Millisecond
	CronTimeout   = 60 * time.Second
	EnableTimeout = 10 * time.Second
)

const (
	// breakerLimit failures in a row pause a plugin; the panel retries it later.
	breakerLimit = 10
	// probation is how many failures in a row pause a plugin a retry brought back.
	probation = 3
	// recycleAt is how much of its heap limit a VM may have grown by (over its size
	// right after loading) before it is replaced after a call: wasm memory never
	// shrinks, so a VM that once spiked would keep the memory otherwise.
	recycleAt = 0.75
)

// retryDelays space the panel's own attempts to bring back a plugin the breaker
// paused or that would not start at boot: the service it calls being down, the
// network not up yet. The last delay repeats.
var retryDelays = []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour}

// notifyEvery spaces the admin notices about one plugin that keeps stopping.
const notifyEvery = 6 * time.Hour

var (
	ErrNotFound  = errors.New("plugin: not installed")
	ErrNotActive = errors.New("plugin: not active")
	ErrExists    = errors.New("plugin: already installed")
	ErrConsent   = errors.New("plugin: the consent does not match the package")
)

// SetupError is a plugin switched on, or that would stay so, without settings it
// cannot run without.
type SetupError struct{ Keys []string }

func (e *SetupError) Error() string {
	return "plugin: fill in the settings first: " + strings.Join(e.Keys, ", ")
}

// Host runs the installed plugins.
type Host struct {
	deps   Deps
	log    *slog.Logger
	engine *jsvm.Engine
	shared bool // engine belongs to another host

	mu      sync.RWMutex
	plugins map[string]*instance
	closed  bool

	cron           cronState
	widgets        widgetCache
	subs           subCache
	decided        decisionCache
	bot            botCache
	subTransforms  transformCache
	failing        sync.Map     // "surface/plugin" → when it last threw (skipFailing)
	lastRelease    atomic.Int64 // unix nanos of the last memory release
	releasePending atomic.Bool  // a release is scheduled
}

// instance is one installed plugin, loaded or not.
type instance struct {
	host *Host
	id   string

	// mu serializes everything that touches the plugin: its calls, and its
	// lifecycle (start, stop, settings) against them.
	mu     gate
	rec    model.Plugin
	pkg    *manifest.Package
	vm     *jsvm.VM
	vmBase uint32 // the VM's memory right after loading
	db     *pdb.DB
	blobs  *blobSet // the running call's panel.blob files
	// running: started (database open, migrations applied, code loaded). A plugin
	// can be "active" by its record before that — the panel starting up.
	running bool
	fails   int
	status  string
	errMsg  string
	// removed: uninstalled while an action waited for mu — it must not bring the
	// plugin back.
	removed bool
	// trips counts the panel's retries since the plugin last did real work;
	// retryAt (unix nanos, 0 none) is when the next is due.
	trips    int
	retryAt  atomic.Int64
	notified time.Time
	// reloading: a reload of the VM runs off the call that needed it (reloadSoon).
	reloading atomic.Bool

	logs  *ring
	fwd   forwardLimit // lines copied into the panel's log
	storm stormMeter

	// pub is what other goroutines may read without waiting on mu, which a long
	// call can hold: whether the plugin is active and what it provides.
	pub atomic.Pointer[published]
}

type published struct {
	status   string
	errMsg   string
	manifest *manifest.Manifest
	pkg      *manifest.Package // read-only once loaded: its strings, for answers given outside a call
	sort     int
	rec      model.Plugin // a copy: what the plugins page shows while a long call holds mu
}

// New creates the host. Nothing runs until Start.
func New(deps Deps) *Host {
	lg := deps.Logger
	if lg == nil {
		lg = slog.Default()
	}
	engine, shared := deps.Engine, deps.Engine != nil
	if !shared {
		engine = jsvm.NewEngine(jsvm.Options{CacheDir: filepath.Join(deps.DataDir, "cache", "wasm")})
	}
	return &Host{
		deps:    deps,
		log:     lg.With("component", "plugins"),
		engine:  engine,
		shared:  shared,
		plugins: map[string]*instance{},
	}
}

// Engine is the host's VM engine, for a sandbox host to share (Deps.Engine).
func (h *Host) Engine() *jsvm.Engine { return h.engine }

// Start loads every installed plugin and starts the enabled ones. A plugin that
// fails to start is marked and logged; Start itself only fails on the store.
func (h *Host) Start(ctx context.Context) error {
	recs, err := h.deps.Store.ListPlugins()
	if err != nil {
		return err
	}
	// Blobs of calls a crash or a kill cut short.
	_ = os.RemoveAll(filepath.Join(h.deps.DataDir, "plugins", ".blobs"))
	// And half-written snapshots or restores.
	pdb.Sweep(filepath.Join(h.deps.DataDir, "plugins"))
	// Every plugin is known (and subscribed) before the first one starts: starting
	// takes a while per plugin, and events keep coming meanwhile.
	insts := make([]*instance, len(recs))
	for i, rec := range recs {
		insts[i] = h.add(rec)
	}
	for i, rec := range recs {
		inst := insts[i]
		if !rec.Enabled {
			continue
		}
		inst.mu.Lock()
		switch {
		case rec.Status == model.PluginPaused && strings.Contains(rec.StatusError, tripMark):
			inst.retryLater() // the breaker's pause goes on being retried
		case rec.Status == model.PluginPaused:
			// Paused for its quota or an event storm: the operator lifts that.
		default:
			if err := inst.start(ctx); err != nil && !errors.Is(err, ErrPaused) {
				d := inst.retryLater()
				h.log.Warn("plugin failed to start", "plugin", rec.ID, "err", err, "retry_in", d)
				inst.notify("failed to start: "+err.Error(), true)
			}
		}
		inst.mu.Unlock()
	}
	return nil
}

// retryLater schedules the panel's next attempt to start the plugin (mu held).
func (inst *instance) retryLater() time.Duration {
	d := retryDelays[min(inst.trips, len(retryDelays)-1)]
	inst.trips++
	inst.retryAt.Store(time.Now().Add(d).UnixNano())
	inst.publish()
	return d
}

// notify tells the admins the plugin stopped — once in a while, not on every retry.
func (inst *instance) notify(reason string, retrying bool) {
	n := inst.host.deps.Notify
	if n == nil || time.Since(inst.notified) < notifyEvery {
		return
	}
	inst.notified = time.Now()
	go n(inst.id, reason, retrying)
}

// RetryStopped brings back the plugins whose retry is due (see retryLater). A
// plugin back on a retry is paused again after a few failures, not ten.
func (h *Host) RetryStopped(ctx context.Context, now time.Time) {
	for _, inst := range h.all() {
		at := inst.retryAt.Load()
		if at == 0 || now.UnixNano() < at {
			continue
		}
		go func() {
			inst.mu.Lock()
			defer inst.mu.Unlock()
			if inst.removed || h.isClosed() || !inst.retryAt.CompareAndSwap(at, 0) {
				return
			}
			if !inst.rec.Enabled || (inst.status != model.PluginPaused && inst.status != model.PluginError) {
				return
			}
			inst.logf("info", "the panel retries the plugin (attempt %d)", inst.trips)
			if err := inst.start(ctx); err != nil {
				if !errors.Is(err, ErrPaused) {
					d := inst.retryLater()
					inst.logf("error", "retry failed: %v — the next in %s", err, d)
				}
				return
			}
			inst.fails = breakerLimit - probation
		}()
	}
}

// all lists the installed plugins.
func (h *Host) all() []*instance {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*instance, 0, len(h.plugins))
	for _, p := range h.plugins {
		out = append(out, p)
	}
	return out
}

// lock finds a plugin and takes its lock. A plugin uninstalled while this waited
// is gone: an Enable queued behind the Uninstall must not bring it back.
func (h *Host) lock(id string) (*instance, error) {
	inst, err := h.get(id)
	if err != nil {
		return nil, err
	}
	inst.mu.Lock()
	if inst.removed {
		inst.mu.Unlock()
		return nil, ErrNotFound
	}
	return inst, nil
}

// Close stops every plugin and the engine.
func (h *Host) Close(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	for _, inst := range h.all() {
		inst.mu.Lock()
		inst.stop()
		inst.status = model.PluginDisabled // a call queued behind this one must not start a VM
		inst.publish()
		inst.mu.Unlock()
	}
	if h.shared {
		return nil
	}
	return h.engine.Close(ctx)
}

func (h *Host) isClosed() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.closed
}

func (h *Host) add(rec model.Plugin) *instance {
	inst := &instance{host: h, id: rec.ID, rec: rec, status: rec.Status, errMsg: rec.StatusError, logs: newRing(logLines), mu: newGate()}
	if !rec.Enabled {
		inst.status = model.PluginDisabled
	}
	inst.publish()
	h.mu.Lock()
	h.plugins[rec.ID] = inst
	h.mu.Unlock()
	return inst
}

func (h *Host) get(id string) (*instance, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	inst := h.plugins[id]
	if inst == nil {
		return nil, ErrNotFound
	}
	return inst, nil
}

func (h *Host) dbPath(id string) string { return filepath.Join(h.deps.DataDir, "plugins", id+".db") }

// --- lifecycle (called with inst.mu held) ---

// start opens the plugin's database, applies its migrations, loads its code and
// calls onEnable. On failure the plugin is left stopped with status error.
func (inst *instance) start(ctx context.Context) error {
	inst.stop()
	wasPaused := inst.status == model.PluginPaused
	err := inst.open(ctx)
	if err == nil && inst.status == model.PluginPaused && !wasPaused {
		err = ErrPaused // onEnable ran but outgrew the quota: it stays paused, not "active"
	}
	if errors.Is(err, ErrPaused) {
		return err
	}
	if err != nil {
		inst.stop()
		inst.setState(true, model.PluginError, err.Error())
		return err
	}
	inst.fails = 0
	inst.running = true
	inst.retryAt.Store(0)
	inst.setState(true, model.PluginActive, "")
	return nil
}

func (inst *instance) open(ctx context.Context) error {
	h := inst.host
	pkg, err := manifest.Read(inst.rec.Package, h.deps.PanelVersion)
	if err != nil {
		return err
	}
	inst.pkg = pkg
	path := h.dbPath(inst.id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	db, err := pdb.Open(ctx, path, pkg.Manifest.Quota())
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	inst.db = db
	if applied, err := db.Migrate(ctx, pkg.Migrations); err != nil {
		return err
	} else if len(applied) > 0 {
		inst.logf("info", "migrations applied: %s", strings.Join(applied, ", "))
	}
	if err := inst.ensureVM(ctx, callOpts{}); err != nil {
		return err
	}
	var missing []string
	for _, e := range pkg.Manifest.Exports() {
		if !inst.vm.Has(ctx, e) {
			missing = append(missing, e)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("main.js does not export what plugin.json declares: %s", strings.Join(missing, ", "))
	}
	if inst.vm.Has(ctx, "onEnable") {
		// On its own clock and marked as this plugin's, like any call: an operator's
		// browser closing must not kill it, and it may not call itself back.
		if _, err := inst.invoke(context.WithoutCancel(withCalling(ctx, inst.id)), "onEnable", nil, EnableTimeout, callOpts{}); err != nil {
			return fmt.Errorf("onEnable: %w", err)
		}
	}
	return nil
}

// reloadSoon loads the VM in the background, under the plugin's lock like any
// call, for a call that could not wait for it.
func (inst *instance) reloadSoon() {
	if !inst.reloading.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer inst.reloading.Store(false)
		inst.mu.Lock()
		defer inst.mu.Unlock()
		if inst.removed || !inst.running || inst.status != model.PluginActive || inst.host.isClosed() {
			return
		}
		if err := inst.ensureVM(context.Background(), callOpts{}); err != nil {
			inst.failed(fmt.Errorf("reload: %w", err))
		}
	}()
}

// ensureVM makes a VM with the prelude and the plugin's code loaded, if there is
// no live one.
// o is the call's: main.js loaded inside a decision may only read, like the call.
func (inst *instance) ensureVM(ctx context.Context, o callOpts) error {
	if inst.vm != nil && inst.vm.Err() == nil {
		return nil
	}
	if inst.vm != nil {
		inst.vm.Close()
		inst.vm = nil
	}
	ctx = context.WithoutCancel(ctx) // the VM outlives the request that happened to start it
	vm, err := inst.host.engine.NewVM(ctx, jsvm.Limits{Memory: inst.pkg.Manifest.Memory()})
	if err != nil {
		return err
	}
	hf := inst.hostFunc(o)
	if err := vm.Script(ctx, "prelude.js", preludeJS, LoadTimeout, hf); err != nil {
		vm.Close()
		return fmt.Errorf("prelude: %w", err)
	}
	if err := vm.Load(ctx, inst.pkg.Main, LoadTimeout, hf); err != nil {
		vm.Close()
		return fmt.Errorf("main.js: %w", err)
	}
	inst.vm = vm
	inst.vmBase = vm.Memory()
	return nil
}

// publish refreshes the lock-free view of the plugin (call with mu held).
func (inst *instance) publish() {
	rec := inst.rec
	rec.Config = maps.Clone(rec.Config)
	p := &published{status: inst.status, errMsg: inst.errMsg, sort: inst.rec.Sort, rec: rec}
	if inst.pkg != nil {
		p.manifest, p.pkg = inst.pkg.Manifest, inst.pkg
	} else if m, err := manifest.Parse([]byte(inst.rec.Manifest)); err == nil {
		// Not loaded yet (the panel starting): what it provides is known from its
		// record, so events for it are queued from the first moment.
		p.manifest = m
	}
	inst.pub.Store(p)
}

func (inst *instance) stop() {
	inst.running = false
	if inst.vm != nil {
		inst.vm.Close()
		inst.vm = nil
		inst.host.releaseMemory()
	}
	if inst.db != nil {
		_ = inst.db.Close()
		inst.db = nil
	}
}

func (inst *instance) setState(enabled bool, status, msg string) {
	inst.status, inst.errMsg = status, msg
	inst.rec.Enabled, inst.rec.Status, inst.rec.StatusError = enabled, status, msg
	inst.publish()
	if err := inst.host.deps.Store.SetPluginState(inst.id, enabled, status, msg); err != nil {
		inst.host.log.Error("plugin state not saved", "plugin", inst.id, "err", err)
	}
}

// pause stops a misbehaving plugin until an operator resumes it: its database
// over quota, an event storm. What waits for it is dropped.
func (inst *instance) pause(reason string) {
	inst.stop()
	inst.retryAt.Store(0)
	inst.setState(true, model.PluginPaused, reason)
	inst.logf("error", "paused: %s", reason)
	inst.host.log.Warn("plugin paused", "plugin", inst.id, "reason", reason)
	inst.notified = time.Time{} // always told: this one waits for the operator
	inst.notify(reason, false)
	inst.host.stopped(inst.id)
}

// stopped tells the panel a plugin no longer takes events.
func (h *Host) stopped(id string) {
	if f := h.deps.Stopped; f != nil {
		go f(id)
	}
}

// --- operator actions ---

// Consent is what the operator saw and approved on the consent screen. Install and
// Update refuse a package that differs from it — the zip, or what it asks for.
type Consent struct {
	SHA256 string   `json:"sha256"`
	Perms  []string `json:"perms"`
	Net    []string `json:"net"`
}

func (c Consent) matches(pkg *manifest.Package) bool {
	return c.SHA256 == pkg.SHA256 && sameSet(c.Perms, pkg.Manifest.Permissions) && sameSet(c.Net, pkg.Manifest.Net)
}

// Inspect reads a package for the consent screen without installing it.
func (h *Host) Inspect(raw []byte) (*manifest.Package, error) {
	return manifest.Read(raw, h.deps.PanelVersion)
}

// Install adds a new plugin, disabled: the operator configures it, then enables it.
func (h *Host) Install(ctx context.Context, raw []byte, consent Consent) (*Info, error) {
	pkg, err := h.Inspect(raw)
	if err != nil {
		return nil, err
	}
	if !consent.matches(pkg) {
		return nil, ErrConsent
	}
	if _, err := h.get(pkg.Manifest.ID); err == nil {
		return nil, ErrExists
	}
	mf, _ := json.Marshal(pkg.Manifest)
	rec := model.Plugin{
		ID: pkg.Manifest.ID, Version: pkg.Manifest.Version, Manifest: string(mf), Package: raw, SHA256: pkg.SHA256,
		Status: model.PluginDisabled, GrantedPerms: pkg.Manifest.Permissions, GrantedNet: pkg.Manifest.Net,
		Config: defaults(pkg.Manifest), Sort: h.nextSort(),
	}
	if err := h.deps.Store.SavePlugin(rec); err != nil {
		return nil, err
	}
	inst := h.add(rec)
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.pkg = pkg
	inst.publish()
	return inst.info(), nil
}

// Update replaces an installed plugin's package. The previous package and a
// snapshot of its database are kept for Rollback; a running plugin restarts on the
// new code and rolls itself back when that fails.
func (h *Host) Update(ctx context.Context, raw []byte, consent Consent) (*Info, error) {
	pkg, err := h.Inspect(raw)
	if err != nil {
		return nil, err
	}
	if !consent.matches(pkg) {
		return nil, ErrConsent
	}
	inst, err := h.lock(pkg.Manifest.ID)
	if err != nil {
		return nil, err
	}
	defer inst.mu.Unlock()
	prev := inst.rec
	mf, _ := json.Marshal(pkg.Manifest)
	next := prev
	next.Version, next.Manifest, next.Package, next.SHA256 = pkg.Manifest.Version, string(mf), raw, pkg.SHA256
	next.GrantedPerms, next.GrantedNet = pkg.Manifest.Permissions, pkg.Manifest.Net
	next.PrevPackage, next.PrevVersion = prev.Package, prev.Version
	next.Config = mergeDefaults(prev.Config, pkg.Manifest)
	// The snapshot first, with the plugin stopped so none of its writes miss it: a
	// rollback restores the data as this version left it. Nothing has changed if it
	// fails — the old version goes on.
	wasRunning := inst.running
	inst.stop()
	path := h.dbPath(inst.id)
	err = os.MkdirAll(filepath.Dir(path), 0o700)
	if err == nil {
		err = pdb.Snapshot(ctx, path, path+".prev")
	}
	if err == nil {
		err = h.deps.Store.SavePlugin(next)
	}
	if err != nil {
		if wasRunning {
			if e := inst.start(ctx); e != nil {
				inst.logf("error", "restarting after a failed update: %v", e)
			}
		}
		return nil, fmt.Errorf("update: %w", err)
	}
	inst.rec = next
	inst.pkg = pkg
	inst.publish()
	if !prev.Enabled {
		return inst.info(), nil
	}
	if pr := pkg.Manifest.Provides; len(pr.Events) == 0 && pr.Channel == nil {
		h.stopped(inst.id) // this version takes no events: what waits for it would wait forever
	}
	if err := inst.start(ctx); err != nil {
		inst.logf("error", "update to %s failed, rolling back: %v", next.Version, err)
		if rbErr := inst.rollback(ctx); rbErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return nil, fmt.Errorf("update failed and was rolled back: %w", err)
	}
	return inst.info(), nil
}

// Rollback restores the previous package and the database snapshot taken before
// the update.
func (h *Host) Rollback(ctx context.Context, id string) (*Info, error) {
	inst, err := h.lock(id)
	if err != nil {
		return nil, err
	}
	defer inst.mu.Unlock()
	if err := inst.rollback(ctx); err != nil {
		return nil, err
	}
	return inst.info(), nil
}

func (inst *instance) rollback(ctx context.Context) error {
	if len(inst.rec.PrevPackage) == 0 {
		return errors.New("plugin: no previous version to roll back to")
	}
	h := inst.host
	pkg, err := manifest.Read(inst.rec.PrevPackage, h.deps.PanelVersion)
	if err != nil {
		return err
	}
	path := h.dbPath(inst.id)
	// The previous code on the data the update migrated would be worse than no
	// rollback: without the snapshot taken before it, refuse.
	if _, err := os.Stat(path + ".prev"); err != nil {
		return errors.New("plugin: the database snapshot taken before the update is gone; roll back by installing the previous version")
	}
	inst.stop()
	if err := pdb.Restore(path+".prev", path); err != nil {
		return fmt.Errorf("restore database: %w", err)
	}
	rec := inst.rec
	mf, _ := json.Marshal(pkg.Manifest)
	rec.Version, rec.Manifest, rec.Package, rec.SHA256 = pkg.Manifest.Version, string(mf), rec.PrevPackage, pkg.SHA256
	rec.GrantedPerms, rec.GrantedNet = pkg.Manifest.Permissions, pkg.Manifest.Net
	rec.PrevPackage, rec.PrevVersion = nil, ""
	if err := h.deps.Store.SavePlugin(rec); err != nil {
		return err
	}
	inst.rec, inst.pkg = rec, pkg
	inst.publish()
	if rec.Enabled {
		return inst.start(ctx)
	}
	return nil
}

// Package is the installed plugin's package, to open in the panel's editor.
func (h *Host) Package(id string) ([]byte, error) {
	inst, err := h.get(id)
	if err != nil {
		return nil, err
	}
	return inst.pub.Load().rec.Package, nil
}

// PrevPermissions are what the version a rollback would restore was granted.
func (h *Host) PrevPermissions(id string) ([]string, error) {
	inst, err := h.get(id)
	if err != nil {
		return nil, err
	}
	prev := inst.pub.Load().rec.PrevPackage
	if len(prev) == 0 {
		return nil, nil
	}
	pkg, err := manifest.Read(prev, "")
	if err != nil {
		return nil, err
	}
	return pkg.Manifest.Permissions, nil
}

// Enable turns a plugin on (or resumes a paused one).
func (h *Host) Enable(ctx context.Context, id string) (*Info, error) {
	inst, err := h.lock(id)
	if err != nil {
		return nil, err
	}
	defer inst.mu.Unlock()
	if missing := inst.missingSettings(); len(missing) > 0 {
		return nil, &SetupError{Keys: missing}
	}
	err = inst.start(ctx)
	return inst.info(), err
}

// Disable turns a plugin off.
func (h *Host) Disable(_ context.Context, id string) (*Info, error) {
	inst, err := h.lock(id)
	if err != nil {
		return nil, err
	}
	defer inst.mu.Unlock()
	inst.stop()
	inst.retryAt.Store(0)
	inst.trips = 0
	inst.setState(false, model.PluginDisabled, "")
	h.stopped(id)
	return inst.info(), nil
}

// Uninstall removes a plugin; its database goes too unless keepData.
func (h *Host) Uninstall(_ context.Context, id string, keepData bool) error {
	inst, err := h.lock(id)
	if err != nil {
		return err
	}
	defer inst.mu.Unlock()
	// The record first: should it fail to go, the plugin runs on as it was, rather
	// than left "active" with nothing running. No call runs meanwhile — mu is held.
	if err := h.deps.Store.DeletePlugin(id); err != nil {
		return err
	}
	inst.stop()
	inst.status = model.PluginDisabled // a call queued behind this one must not run it again
	inst.removed = true
	inst.retryAt.Store(0)
	inst.publish()
	h.mu.Lock()
	delete(h.plugins, id)
	h.mu.Unlock()
	h.stopped(id)
	if !keepData {
		return pdb.Remove(h.dbPath(id))
	}
	return nil
}

// SetConfig saves the plugin's settings. A field not sent keeps its value, an empty
// one clears it — except a secret, where empty keeps the stored one, as the payment
// forms do; a secret goes only when named in clear. A running plugin restarts on the
// new settings.
func (h *Host) SetConfig(ctx context.Context, id string, values map[string]string, clear ...string) (*Info, error) {
	inst, err := h.lock(id)
	if err != nil {
		return nil, err
	}
	defer inst.mu.Unlock()
	m, err := inst.manifest()
	if err != nil {
		return nil, err
	}
	cfg, err := validateConfig(m, inst.rec.Config, values, clear...)
	if err != nil {
		return nil, err
	}
	// A plugin that is on keeps what it needs: emptying a required setting would
	// leave it failing every call until the breaker pauses it. Switch it off first.
	if missing := missingSettings(m, cfg); inst.rec.Enabled && len(missing) > 0 {
		return nil, &SetupError{Keys: missing}
	}
	if err := h.deps.Store.SetPluginConfig(id, cfg); err != nil {
		return nil, err
	}
	inst.rec.Config = cfg
	inst.publish()
	if inst.rec.Enabled && inst.status == model.PluginActive {
		err = inst.start(ctx)
	}
	return inst.info(), err
}

func (inst *instance) manifest() (*manifest.Manifest, error) {
	if inst.pkg != nil {
		return inst.pkg.Manifest, nil
	}
	pkg, err := manifest.Read(inst.rec.Package, inst.host.deps.PanelVersion)
	if err != nil {
		// A package the panel has outgrown still shows; its manifest is stored.
		return manifest.Parse([]byte(inst.rec.Manifest))
	}
	inst.pkg = pkg
	return pkg.Manifest, nil
}

func (inst *instance) missingSettings() []string {
	m, err := inst.manifest()
	if err != nil {
		return nil
	}
	return missingSettings(m, inst.rec.Config)
}

// missingSettings are the required settings cfg leaves empty.
func missingSettings(m *manifest.Manifest, cfg map[string]string) []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, f := range m.Settings {
		if !f.Optional && f.Kind != "bool" && strings.TrimSpace(cfg[f.Key]) == "" {
			out = append(out, f.Key)
		}
	}
	return out
}

func (h *Host) nextSort() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, inst := range h.plugins {
		if p := inst.pub.Load(); p != nil && p.sort >= n {
			n = p.sort + 1
		}
	}
	return n
}

// --- what the admin panel shows ---

// Info is one plugin as the admin panel lists it.
type Info struct {
	ID           string             `json:"id"`
	Version      string             `json:"version"`
	Manifest     *manifest.Manifest `json:"manifest"`
	Enabled      bool               `json:"enabled"`
	Status       string             `json:"status"`
	StatusError  string             `json:"status_error,omitempty"`
	Config       map[string]string  `json:"config"` // secrets blanked
	SecretsSet   []string           `json:"secrets_set,omitempty"`
	PrevVersion  string             `json:"prev_version,omitempty"`
	InstalledAt  int64              `json:"installed_at"`
	UpdatedAt    int64              `json:"updated_at"`
	SHA256       string             `json:"sha256"`
	DBBytes      int64              `json:"db_bytes"` // with its write-ahead log and rollback snapshot
	MissingSetup []string           `json:"missing_setup,omitempty"`
	RetryAt      int64              `json:"retry_at,omitempty"` // unix: the panel's next attempt to bring it back
	// HTTPURL is where onHttp answers; PaymentKey the provider key of its payment
	// method and PaymentWebhook the callback URL to give the payment system.
	HTTPURL        string `json:"http_url,omitempty"`
	PaymentKey     string `json:"payment_key,omitempty"`
	PaymentWebhook string `json:"payment_webhook,omitempty"`
}

// info is the plugin as published (call with mu held, after the change it shows).
func (inst *instance) info() *Info { return inst.host.infoOf(inst) }

// infoOf builds what the admin panel shows from a published view: no waiting on a
// plugin in a long call.
func (h *Host) infoOf(inst *instance) *Info {
	id, p := inst.id, inst.pub.Load()
	rec, m := p.rec, p.manifest
	in := &Info{
		ID: id, Version: rec.Version, Manifest: m, Enabled: rec.Enabled,
		Status: p.status, StatusError: p.errMsg, PrevVersion: rec.PrevVersion,
		InstalledAt: rec.InstalledAt, UpdatedAt: rec.UpdatedAt, SHA256: rec.SHA256,
		Config: map[string]string{}, MissingSetup: missingSettings(m, rec.Config),
	}
	if at := inst.retryAt.Load(); at != 0 {
		in.RetryAt = time.Unix(0, at).Unix()
	}
	if m != nil {
		for _, f := range m.Settings {
			v := rec.Config[f.Key]
			if f.Kind == "secret" {
				if v != "" {
					in.SecretsSet = append(in.SecretsSet, f.Key)
				}
				continue
			}
			in.Config[f.Key] = v
		}
	}
	in.DBBytes = h.diskBytes(id)
	if m != nil {
		pub := h.deps.PublicURL
		if m.Provides.HTTP && pub != nil {
			in.HTTPURL = pub("x/" + id)
		}
		if m.Provides.Payment != nil {
			in.PaymentKey = PaymentKeyPrefix + id
			if pub != nil {
				in.PaymentWebhook = pub(in.PaymentKey)
			}
		}
	}
	return in
}

// diskBytes is what the plugin's data takes on disk: its database with the
// write-ahead log and the snapshot kept for a rollback.
func (h *Host) diskBytes(id string) int64 {
	var n int64
	for _, suffix := range []string{"", "-wal", ".prev"} {
		if st, err := os.Stat(h.dbPath(id) + suffix); err == nil {
			n += st.Size()
		}
	}
	return n
}

// List returns every installed plugin in order.
func (h *Host) List() []*Info {
	h.mu.RLock()
	insts := make([]*instance, 0, len(h.plugins))
	for _, p := range h.plugins {
		insts = append(insts, p)
	}
	h.mu.RUnlock()
	sortOf := func(inst *instance) int {
		if p := inst.pub.Load(); p != nil {
			return p.sort
		}
		return 0
	}
	sort.Slice(insts, func(i, j int) bool {
		if a, b := sortOf(insts[i]), sortOf(insts[j]); a != b {
			return a < b
		}
		return insts[i].id < insts[j].id
	})
	out := make([]*Info, 0, len(insts))
	for _, inst := range insts {
		out = append(out, h.infoOf(inst))
	}
	return out
}

// Get returns one plugin.
func (h *Host) Get(id string) (*Info, error) {
	inst, err := h.get(id)
	if err != nil {
		return nil, err
	}
	return h.infoOf(inst), nil
}

// Code returns the plugin's main.js, for the code view.
func (h *Host) Code(id string) (string, error) {
	inst, err := h.get(id)
	if err != nil {
		return "", err
	}
	pkg, err := manifest.Read(inst.pub.Load().rec.Package, "")
	if err != nil {
		return "", err
	}
	return pkg.Main, nil
}

// Logs returns the plugin's recent log lines, oldest first.
func (h *Host) Logs(id string) ([]LogLine, error) {
	inst, err := h.get(id)
	if err != nil {
		return nil, err
	}
	return inst.logs.lines(), nil
}

// Active lists the active plugins that provide a point, in order. which picks the
// point out of a manifest.
func (h *Host) Active(which func(*manifest.Manifest) bool) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	type item struct {
		id   string
		sort int
	}
	var items []item
	for id, inst := range h.plugins {
		p := inst.pub.Load()
		if p == nil || p.status != model.PluginActive || p.manifest == nil || !which(p.manifest) {
			continue
		}
		items = append(items, item{id, p.sort})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].sort != items[j].sort {
			return items[i].sort < items[j].sort
		}
		return items[i].id < items[j].id
	})
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}

// CheckpointAll folds every plugin database's WAL into its file, before a backup
// copies the data directory (which skips -wal files).
func (h *Host) CheckpointAll(ctx context.Context) {
	h.mu.RLock()
	insts := make([]*instance, 0, len(h.plugins))
	for _, p := range h.plugins {
		insts = append(insts, p)
	}
	h.mu.RUnlock()
	for _, inst := range insts {
		// Bounded: a backup must not hang behind a plugin stuck in a long call. That
		// plugin's last writes then miss this backup and make the next one.
		if !inst.mu.lockWithin(ctx, 10*time.Second) {
			h.log.Warn("plugin db checkpoint skipped: the plugin is busy", "plugin", inst.id)
			continue
		}
		if inst.db != nil {
			if err := inst.db.Checkpoint(ctx); err != nil {
				h.log.Warn("plugin db checkpoint", "plugin", inst.id, "err", err)
			}
		}
		inst.mu.Unlock()
	}
}

// --- settings ---

func defaults(m *manifest.Manifest) map[string]string {
	cfg := map[string]string{}
	for _, f := range m.Settings {
		switch {
		case f.Kind == "bool":
			cfg[f.Key] = manifest.CanonBool(f.Default)
		case f.Default != "":
			cfg[f.Key] = f.Default
		}
	}
	return cfg
}

// mergeDefaults keeps the values of settings the new version still has and adds
// the defaults of new ones.
func mergeDefaults(old map[string]string, m *manifest.Manifest) map[string]string {
	cfg := defaults(m)
	for _, f := range m.Settings {
		if v, ok := old[f.Key]; ok {
			cfg[f.Key] = v
		}
	}
	return cfg
}

// validateConfig checks submitted settings against the manifest's fields and
// returns the config to store.
func validateConfig(m *manifest.Manifest, old, values map[string]string, clear ...string) (map[string]string, error) {
	cfg := map[string]string{}
	var problems []string
	for k := range values {
		if !slices.ContainsFunc(m.Settings, func(f manifest.Field) bool { return f.Key == k }) {
			problems = append(problems, k+": not a setting of this plugin")
		}
	}
	for _, f := range m.Settings {
		raw, present := values[f.Key]
		v := strings.TrimSpace(raw)
		if !present || (f.Kind == "secret" && v == "") {
			v = old[f.Key] // a field not sent, or a blank secret, keeps what is stored
		}
		if slices.Contains(clear, f.Key) {
			v = "" // asked to go, a secret too
		}
		switch f.Kind {
		case "bool":
			if v = manifest.CanonBool(v); v == "" {
				problems = append(problems, f.Key+": must be true or false")
			}
		case "number":
			if v != "" {
				var x float64
				if _, err := fmt.Sscan(v, &x); err != nil {
					problems = append(problems, f.Key+": must be a number")
				}
			}
		case "select":
			if v != "" && !slices.ContainsFunc(f.Options, func(o manifest.Option) bool { return o.Value == v }) {
				problems = append(problems, f.Key+": not one of the options")
			}
		}
		if len(v) > 16<<10 {
			problems = append(problems, f.Key+": too long")
		}
		if v != "" {
			cfg[f.Key] = v
		}
	}
	if len(problems) > 0 {
		return nil, manifest.Problems(problems)
	}
	return cfg, nil
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

// DevOp runs one host operation for a plugin outside any call — what the author
// tools (internal/plugin/devkit) use to seed and inspect a plugin's database from a
// test. Not reachable from the panel's own surfaces.
func (h *Host) DevOp(ctx context.Context, id, op string, arg []byte) ([]byte, error) {
	inst, err := h.lock(id)
	if err != nil {
		return nil, err
	}
	defer inst.mu.Unlock()
	if inst.pkg == nil {
		return nil, ErrNotActive
	}
	res, err := inst.op(ctx, op, arg, callOpts{})
	if inst.db != nil {
		inst.db.EndCall()
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}
