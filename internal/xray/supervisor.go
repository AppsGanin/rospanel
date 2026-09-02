package xray

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AppsGanin/rospanel/internal/logbuf"
)

// Process supervision tuning.
const (
	validateTimeout = 30 * time.Second // `xray -test` config validation (geosite.dat parse ~7-8s on 1 vCPU)
	statsTimeout    = 10 * time.Second // `xray api statsquery`
	restartBackoff  = time.Second      // base crash-restart delay (doubles, capped)
	maxBackoff      = 30 * time.Second
	healthyUptime   = 30 * time.Second // a run longer than this resets the backoff

	// Wedged-process watchdog: the exit monitor restarts a process that DIES; this
	// covers one that stays alive but stops serving (Xray's API stops answering).
	watchdogInterval   = 30 * time.Second // how often to probe a running process
	watchdogFailsToAct = 3                // consecutive failed probes before restarting (~90s wedged)
	watchdogCooldown   = 5 * time.Minute  // min gap between watchdog restarts (anti-storm)
)

// proc is a single running Xray child. done is closed once Wait() has reaped it;
// stop marks an intentional kill so the monitor won't restart it.
type proc struct {
	cmd     *exec.Cmd
	done    chan struct{}
	started time.Time
	stop    bool
	// cfg is the config file as it was when this process started, kept so a run that
	// proves itself can be promoted to the rollback copy. Read here rather than taken
	// from appliedCfg because a supervised restart starts from the file without going
	// through Apply, so appliedCfg can describe a different config than the one this
	// process is actually running. nil when the file could not be read: then there is
	// simply nothing to promote.
	cfg []byte
}

// Supervisor owns the Xray child process and the on-disk config.json. It
// serializes config applies: marshal -> validate -> atomic swap -> restart, and
// supervises the process — an unexpected exit is auto-restarted with backoff.
//
// If no Xray binary is available (e.g. local dev on macOS), Apply still writes
// and the panel keeps running; it just logs that Xray isn't being (re)started.
type Supervisor struct {
	bin        string // resolved binary path, or "" if unavailable
	configPath string
	assetDir   string // XRAY_LOCATION_ASSET (geoip.dat / geosite.dat)

	runMu sync.Mutex // serializes whole start/stop/apply operations

	mu        sync.Mutex // guards the fields below
	cur       *proc      // currently-running process, or nil if down
	closed    bool       // panel is shutting down; do not (re)start, ever
	restarts  int        // consecutive crash restarts (backoff exponent)
	lastApply time.Time  // when the last Apply() succeeded (zero if never)
	// rolledBack marks that the config on disk has already been reverted once for the
	// current generation, so a backup that is itself unusable cannot start a loop.
	// Cleared by Apply/ApplyRaw: a new config is a new generation and deserves its own
	// attempt.
	rolledBack bool
	// lastLoadErr is why the config on disk was rejected, kept for the message that
	// tells the operator their change was reverted and what was wrong with it.
	lastLoadErr string
	// appliedCfg is the config the RUNNING process was started with. Apply compares
	// against this, not against the file: WriteConfig deliberately moves the file
	// ahead of the process (see its doc), so a file comparison would read "nothing
	// changed" for precisely the change that still needs a restart.
	appliedCfg []byte
	// suspended is Stop's reversible twin: Xray is meant to stay down until somebody
	// deliberately starts it again. It has to be checked everywhere `closed` is,
	// because the crash supervisor is otherwise perfectly happy to bring Xray back
	// up on a node the panel just revoked — which is exactly what revoking is meant
	// to prevent (it runs the last config, with credentials we have withdrawn).
	suspended  bool
	restarting bool // a deliberate stop→start is in flight (the ~1s bounce)

	// lastWatchdog is when the wedged-process watchdog last restarted Xray, for its
	// anti-storm cooldown (zero until it fires). watchdogRestarts counts how many times
	// it has fired, for the operator-facing "auto-recovery" indicator.
	lastWatchdog     time.Time
	watchdogRestarts int
	// watchdogDisabled turns the auto-recovery off. Zero value = enabled, so a bare
	// Supervisor watches by default; an operator can switch it off (master only).
	watchdogDisabled atomic.Bool
	// probe reports whether Xray is answering its API; defaults to apiResponsive and is
	// swappable in tests so the watchdog decision can be exercised without a live Xray.
	probe func() bool

	onAccess func(email, ip, dest string) // called per access-log connection line
	onCrash  func(err error)              // called when Xray exits unexpectedly (crash)
	onWedged func(restarted bool)         // called when the watchdog sees a wedged process; restarted=false when auto-recovery is off (alert only)
	// onRecover is called when a SUPERVISED restart succeeds — i.e. Xray is back up
	// after a crash. Deliberately not fired by Apply-driven restarts (a reconcile, a
	// renewed certificate): those are routine, and reporting them as recovery would
	// make the one message that means "the outage is over" meaningless.
	onRecover func()
	// onRolledBack is called after the config was reverted to its backup, with the
	// reason. Separate from onRecover: coming back up and having a change undone are
	// different facts, and only one of them needs the operator to go look at something.
	onRolledBack func(reason string)

	verOnce sync.Once
	version string

	logs *logbuf.Hub // recent Xray log lines + live subscribers
}

// LogTail returns the buffered recent Xray log lines.
func (s *Supervisor) LogTail() []string { return s.logs.Tail() }

// SubscribeLogs returns a channel of new Xray log lines and an unsubscribe func.
func (s *Supervisor) SubscribeLogs() (<-chan string, func()) { return s.logs.Subscribe() }

// UptimeSeconds reports how long the current Xray process has been running, or 0
// if it's down.
func (s *Supervisor) UptimeSeconds() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return 0
	}
	return int64(time.Since(s.cur.started).Seconds())
}

// StartedAt returns the unix start time of the current Xray process, or 0 if it's
// down. It changes on every (re)start, so clients can detect a config reload.
func (s *Supervisor) StartedAt() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return 0
	}
	return s.cur.started.Unix()
}

// Version returns the Xray version string (e.g. "26.6.1"), cached after the
// first call. Empty when no binary is available.
func (s *Supervisor) Version() string {
	if s.bin == "" {
		return ""
	}
	s.verOnce.Do(func() {
		out, err := exec.Command(s.bin, "version").Output()
		if err != nil {
			return
		}
		line := strings.SplitN(string(out), "\n", 2)[0]
		if f := strings.Fields(line); len(f) >= 2 {
			s.version = f[1]
		}
	})
	return s.version
}

// SetOnAccess registers a callback invoked for each Xray access-log line that
// carries a user email + source IP (used to track online status / connections).
// dest is the destination host when the line has a usable one, else empty.
func (s *Supervisor) SetOnAccess(fn func(email, ip, dest string)) { s.onAccess = fn }

// SetOnCrash registers a callback invoked when the Xray child exits unexpectedly
// (a genuine crash, not an intentional Stop/Apply). Used to alert the operator.
func (s *Supervisor) SetOnCrash(fn func(err error)) { s.onCrash = fn }

// SetOnRecover registers a callback invoked when Xray comes back after a crash.
func (s *Supervisor) SetOnRecover(fn func()) { s.onRecover = fn }

// SetOnRolledBack registers a callback invoked when the config was reverted to its
// backup, with the reason the live one was refused.
func (s *Supervisor) SetOnRolledBack(fn func(reason string)) { s.onRolledBack = fn }

// SetOnWedged registers a callback invoked when the watchdog detects a wedged process
// (alive but no longer serving). Used to alert the operator — this is an outage the crash
// path never reported, because the process never exited. It fires even when auto-recovery
// is OFF (restarted=false), so disabling the auto-restart doesn't blind the operator.
func (s *Supervisor) SetOnWedged(fn func(restarted bool)) { s.onWedged = fn }

// SetWatchdogEnabled turns the wedged-process auto-recovery on or off, live. Off, the
// loop keeps running (so it can be turned back on) but never restarts anything.
func (s *Supervisor) SetWatchdogEnabled(on bool) { s.watchdogDisabled.Store(!on) }

// WatchdogStats reports the auto-recovery state for the operator: whether it is
// enabled, how many times it has restarted a wedged Xray, and when it last did (zero
// if never).
func (s *Supervisor) WatchdogStats() (enabled bool, restarts int, last time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.watchdogDisabled.Load(), s.watchdogRestarts, s.lastWatchdog
}

// StartWatchdog launches the wedged-process watchdog in the background: it probes a
// running Xray and, if the process is alive but its API has stopped answering for
// several checks in a row, restarts it. This is the gap the exit monitor cannot
// cover — that one only sees a process that DIES. Idempotent-ish (call once); a
// missing binary makes it a no-op. The loop exits when the supervisor is Stopped.
func (s *Supervisor) StartWatchdog() {
	if s.bin == "" {
		return
	}
	go s.watchdogLoop()
}

func (s *Supervisor) watchdogLoop() {
	if s.probe == nil {
		s.probe = s.apiResponsive
	}
	t := time.NewTicker(watchdogInterval)
	defer t.Stop()
	fails := 0
	for range t.C {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
		var alert, restart bool
		fails, alert, restart = s.watchdogTick(fails)
		if !alert {
			continue
		}
		// A wedge was seen (past the cooldown). Tell the operator either way; only
		// auto-restart when the toggle allows it.
		if restart {
			slog.Error("xray watchdog: wedged (alive but not serving) — restarting")
		} else {
			slog.Error("xray watchdog: wedged (alive but not serving) — auto-recovery off, not restarting")
		}
		if s.onWedged != nil {
			go s.onWedged(restart)
		}
		if restart {
			if err := s.Restart(); err != nil {
				slog.Error("xray watchdog: restart failed", "err", err)
			}
		}
	}
}

// watchdogTick evaluates one probe cycle and returns the updated consecutive-failure
// count, whether to ALERT the operator to a wedge now, and whether to auto-RESTART now.
// The decision, minus the ticker and the restart itself, so it is unit-testable without a
// live Xray. Detection and alerting are independent of the auto-recovery toggle — a wedge
// is always detected and reported once per cooldown; the toggle only gates the restart,
// so disabling auto-recovery to debug a wedge by hand does not blind the operator:
//   - a down / suspended / mid-bounce supervisor is never judged (a routine restart is
//     not a wedge), and resets the counter;
//   - a responsive process resets the counter;
//   - only after watchdogFailsToAct failures in a row, and past the cooldown since the
//     last action, does it say to alert (recording the action time); restart piggybacks
//     on that same signal but only when the toggle is on (and only then is it counted).
func (s *Supervisor) watchdogTick(fails int) (count int, alert, restart bool) {
	s.mu.Lock()
	watch := s.cur != nil && !s.suspended && !s.restarting
	s.mu.Unlock()
	if !watch {
		return 0, false, false
	}
	probe := s.probe
	if probe == nil {
		probe = s.apiResponsive
	}
	if probe() {
		return 0, false, false
	}
	fails++
	if fails < watchdogFailsToAct {
		slog.Warn("xray watchdog: process alive but not answering its API", "fails", fails)
		return fails, false, false
	}
	// Wedged for watchdogFailsToAct probes in a row. Honour a cooldown so a process
	// that wedges again right after we act can't spin us into a restart/alert storm.
	s.mu.Lock()
	defer s.mu.Unlock()
	// probe() ran with the lock released; re-check that the process is still one we should
	// judge. An operator Suspend (or a restart) that landed during the probe must not be
	// read as a wedge — that would over-count a restart and fire a spurious alert.
	if s.cur == nil || s.suspended || s.restarting {
		return 0, false, false
	}
	if !s.lastWatchdog.IsZero() && time.Since(s.lastWatchdog) < watchdogCooldown {
		return fails, false, false // still wedged; hold off until the cooldown elapses
	}
	s.lastWatchdog = time.Now()
	restart = !s.watchdogDisabled.Load()
	if restart {
		s.watchdogRestarts++ // only actual restarts are counted for WatchdogStats
	}
	return 0, true, restart
}

// apiResponsive reports whether the running Xray still answers its API — a failed,
// timeout-bounded stats query is the "wedged" signal the exit monitor never sees.
func (s *Supervisor) apiResponsive() bool {
	_, err := s.QueryStats(s.APIAddr())
	return err == nil
}

// recovered fires the recovery callback off the restart path, mirroring onCrash.
func (s *Supervisor) recovered() {
	if s.onRecover != nil {
		go s.onRecover()
	}
}

// NewSupervisor resolves binName (via PATH or as an absolute path) and targets
// configPath for the generated config. assetDir holds the geo databases.
func NewSupervisor(binName, configPath, assetDir string) *Supervisor {
	bin := ""
	if binName != "" {
		if p, err := exec.LookPath(binName); err == nil {
			bin = p
		} else if fi, statErr := os.Stat(binName); statErr == nil && !fi.IsDir() {
			bin = binName
		}
	}
	if bin == "" {
		slog.Warn("xray: binary not found; config will be generated but Xray won't be started", "binary", binName)
	}
	return &Supervisor{bin: bin, configPath: configPath, assetDir: assetDir, logs: logbuf.New()}
}

// ConfigBytes returns the on-disk config.json currently applied to Xray.
func (s *Supervisor) ConfigBytes() ([]byte, error) { return os.ReadFile(s.configPath) }

// AssetDir returns the directory holding the geoip.dat / geosite.dat databases.
func (s *Supervisor) AssetDir() string { return s.assetDir }

// BinPath returns the resolved Xray binary, or "" if none was found. The self-test
// spawns a throwaway Xray client from the same binary, so it needs this.
func (s *Supervisor) BinPath() string { return s.bin }

// APIAddr is the loopback address of Xray's gRPC API (StatsService + live user
// add/remove). The wiring lives here so callers don't rebuild it ad hoc.
func (s *Supervisor) APIAddr() string { return fmt.Sprintf("127.0.0.1:%d", APIPort) }

func (s *Supervisor) env() []string {
	env := os.Environ()
	if s.assetDir != "" {
		env = append(env, "XRAY_LOCATION_ASSET="+s.assetDir)
	}
	// Soft heap ceiling for the Go-based xray: GC reclaims harder near the limit so
	// a traffic spike can't balloon RSS on a small box. It's a SOFT limit — the
	// runtime exceeds it rather than OOM-killing if the live heap genuinely needs
	// more, so it can't break xray.
	env = append(env, "GOMEMLIMIT=256MiB")
	if tz := childTZ(); tz != "" {
		env = append(env, "TZ="+tz)
	}
	return env
}

// childTZ is the zone to run Xray in, so the timestamps it stamps on its OWN log
// lines match the panel's — otherwise one log interleaves two zones (Xray in the
// server's system zone, the panel in the operator's).
//
// Returns "" — leaving Xray on the server default — unless the zone is BOTH
// configured by the operator and present in the system zoneinfo. That second check
// matters: xray is a stock Go binary that (unlike ours) may not embed tzdata, and
// Go silently falls back to UTC for a TZ it can't load. Setting a zone blindly
// could therefore push Xray further from the operator's clock, not closer.
func childTZ() string {
	loc := logbuf.Location()
	if loc == nil || loc == time.Local || loc == time.UTC {
		return ""
	}
	name := loc.String()
	if name == "" || name == "Local" {
		return ""
	}
	if _, err := os.Stat(filepath.Join("/usr/share/zoneinfo", name)); err != nil {
		return "" // host has no tzdata for it → don't risk Xray defaulting to UTC
	}
	return name
}

// LastApply is when a config was last applied, whether or not that required a
// restart. The UI waits on this: with Apply now able to be a no-op, waiting only for
// a newer start time would leave the "applying…" modal spinning out its full timeout
// every time a save turned out not to change the config.
func (s *Supervisor) LastApply() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastApply.IsZero() {
		return 0
	}
	return s.lastApply.Unix()
}

// Running reports whether the Xray child process is currently up. Reflects
// reality: a crashed process clears s.cur until a restart succeeds. Callers that
// drive logic (health decisions, auto-restart) want this exact instantaneous truth.
func (s *Supervisor) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur != nil
}

// Serving reports whether Xray is up OR in the middle of a deliberate restart —
// the answer to "is this server working", not "is a process alive this instant".
//
// It exists for status REPORTING, where Running() lies by omission. A restart
// (operator button, cert renewal, config push) holds the process down for about a
// second; a node's sync landing in that window would report xray_running=false and
// paint the server as down for a full poll cycle, for what is really a healthy
// bounce. A crash is different — that clears s.cur without setting restarting, so
// Serving is false for it, which is correct: a crash IS an outage.
func (s *Supervisor) Serving() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Suspended is never "serving", whatever else is true. A switched-off node still
	// applies its config on boot, and that apply sets the bounce flag for a moment —
	// long enough to report a revoked server as up if this didn't say otherwise.
	return !s.suspended && (s.cur != nil || s.restarting)
}

// ValidateBytes runs `xray run -test` over a config WITHOUT applying it, and returns
// Xray's own complaint when it refuses.
//
// This is what lets the panel reject a bad advanced setting in the editor instead of
// storing it, pushing it, and finding out from a crashed Xray and a rollback. Nothing
// but Xray can answer the question — the settings it accepts are defined by its
// parser, and a panel-side whitelist only catches misspelled keys, not values it
// dislikes.
//
// A missing binary means "cannot judge", not "invalid": the caller then falls back to
// its own checks rather than blocking every save.
func (s *Supervisor) ValidateBytes(data []byte) error {
	if s == nil || s.bin == "" {
		return nil
	}
	f, err := os.CreateTemp("", "xray-check-*.json")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
	defer cancel()
	// -format json for the same reason Apply passes it: Xray infers the format from
	// the file extension, and a temp file's is not to be relied on.
	cmd := exec.CommandContext(ctx, s.bin, "run", "-test", "-format", "json", "-c", name)
	cmd.Env = s.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", cleanValidationError(string(out)))
	}
	return nil
}

// cleanValidationError reduces `xray -test` output to the part an operator can act
// on. Raw, it opens with a version banner and names a temp file — noise in front of
// the one sentence that says what is actually wrong.
func cleanValidationError(out string) string {
	var kept []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "", strings.HasPrefix(line, "Xray "),
			strings.HasPrefix(line, "A unified platform"):
			continue
		}
		// Xray nests causes as "outer > inner > root"; the root is the useful one, and
		// the outer frames repeat the temp path.
		if i := strings.LastIndex(line, "> "); i >= 0 {
			line = strings.TrimSpace(line[i+2:])
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return strings.TrimSpace(out)
	}
	return strings.Join(kept, "; ")
}

// ValidateConfig is ValidateBytes for a config still in struct form.
func (s *Supervisor) ValidateConfig(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return s.ValidateBytes(data)
}

// Apply writes the config atomically (validating first when possible) and
// restarts Xray — unless the config it generated is the one already running, in
// which case it does nothing at all.
//
// That check matters far more than it looks. A restart drops EVERY live VPN
// connection, and the panel itself is served through Xray (:443 → the VLESS
// fallback → the loopback panel), so it also kills the admin's browser connection
// mid-request. Meanwhile plenty of saves reach here without changing the generated
// config: renaming a lane, switching a client fingerprint, toggling TLS fragment or
// block-QUIC — all of those live only in the subscription links, never in this file.
// Every one of them used to bounce every user off the VPN and hand the operator a
// "Failed to fetch" for a save that had in fact succeeded.
func (s *Supervisor) Apply(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	// Already running exactly this: nothing to do. Compared against what the live
	// process was started with — NOT the file on disk, which WriteConfig moves ahead
	// of the process on purpose. Both halves are required: with Xray down this is how
	// it gets started, so a match must not stop us.
	s.mu.Lock()
	sameAsRunning := s.cur != nil && s.appliedCfg != nil && bytes.Equal(s.appliedCfg, data)
	if sameAsRunning {
		s.lastApply = time.Now()
		s.rolledBack = false
	}
	s.mu.Unlock()
	if sameAsRunning {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(s.configPath), 0o700); err != nil {
		return err
	}
	tmp := s.configPath + ".new"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}

	if s.bin != "" {
		// -format json: the temp file lacks a .json extension, and Xray otherwise
		// infers config format from the extension. A timeout keeps a wedged
		// validation from holding s.mu (and thus blocking all future applies).
		// validation under runMu (not mu) — a wedged -test can't block Running().
		ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
		cmd := exec.CommandContext(ctx, s.bin, "run", "-test", "-format", "json", "-c", tmp)
		cmd.Env = s.env()
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("xray config validation failed: %w\n%s", err, out)
		}
	}

	// Preserve the current config as a rollback point before overwriting.
	if cur, err := os.ReadFile(s.configPath); err == nil {
		_ = os.WriteFile(s.configPath+".bak", cur, 0o600)
	}
	if err := os.Rename(tmp, s.configPath); err != nil {
		return err
	}
	if err := s.restart(); err != nil {
		return err
	}
	s.mu.Lock()
	s.lastApply = time.Now()
	s.rolledBack = false
	// Remember what the process now runs, so the next Apply can tell a real change
	// from a re-application of the same thing.
	s.appliedCfg = data
	s.mu.Unlock()
	return nil
}

// ApplyRawIfChanged writes and applies data only when it differs from the config
// already on disk, reporting whether it did.
//
// This exists because the node's desired state carries more than the Xray config —
// certificates, hop ranges, the connection guard, per-user speed caps — and ANY
// change to it re-runs the whole apply. Routing an unchanged config through
// ApplyRaw restarts Xray, which drops every live connection on that node: changing
// one user's speed limit would bounce the whole fleet.
//
// The "config unchanged" shortcut is conditional on Xray actually running. A stopped
// process with a matching config on disk still needs starting, and skipping that
// would leave the node dark until something else happened to reload it.
func (s *Supervisor) ApplyRawIfChanged(data []byte) (bool, error) {
	if s.Running() {
		if cur, err := os.ReadFile(s.configPath); err == nil && bytes.Equal(cur, data) {
			return false, nil
		}
	}
	return true, s.ApplyRaw(data)
}

// ApplyRaw is Apply for a config that is already marshaled JSON — used by the node
// agent, which receives the exact config the panel generated and applies it
// verbatim (after substituting its own cert paths) rather than round-tripping it
// through the Config struct. Same validate → atomic swap → restart, with the same
// rollback backup, so a config the node's Xray can't parse never kills it.
func (s *Supervisor) ApplyRaw(data []byte) error {
	s.runMu.Lock()
	defer s.runMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.configPath), 0o700); err != nil {
		return err
	}
	tmp := s.configPath + ".new"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if s.bin != "" {
		ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
		cmd := exec.CommandContext(ctx, s.bin, "run", "-test", "-format", "json", "-c", tmp)
		cmd.Env = s.env()
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("xray config validation failed: %w\n%s", err, out)
		}
	}
	if cur, err := os.ReadFile(s.configPath); err == nil {
		_ = os.WriteFile(s.configPath+".bak", cur, 0o600)
	}
	if err := os.Rename(tmp, s.configPath); err != nil {
		return err
	}
	if err := s.restart(); err != nil {
		return err
	}
	s.mu.Lock()
	s.lastApply = time.Now()
	s.rolledBack = false
	// Keep the invariant Apply relies on: appliedCfg is whatever the running process
	// was started with. Nodes only ever come through here, but leaving it stale would
	// be a trap for the first caller that mixes the two paths.
	s.appliedCfg = data
	s.mu.Unlock()
	return nil
}

// Restart stops the running Xray and starts a fresh one from the config already on
// disk. Unlike Apply it neither regenerates nor re-validates the config — it is the
// operator's "kick it" button for a wedged or misbehaving process, and it also
// makes a process-level change (e.g. the TZ the child runs in) take effect without
// waiting for the next config change.
//
// Every live VPN connection is dropped, so this is only ever operator-initiated.
func (s *Supervisor) Restart() error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.restart()
}

// HasBackup reports whether a rollback config (config.json.bak) is available.
func (s *Supervisor) HasBackup() bool {
	_, err := os.Stat(s.configPath + ".bak")
	return err == nil
}

// restoreBackupLocked promotes config.json.bak → config.json and restarts
// Xray. Caller must hold s.runMu. Validation is skipped (the backup was the
// last known-good config and was already validated when applied).
func (s *Supervisor) restoreBackupLocked() error {
	bak := s.configPath + ".bak"
	data, err := os.ReadFile(bak)
	if err != nil {
		return fmt.Errorf("no backup config found")
	}
	tmp := s.configPath + ".new"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.configPath); err != nil {
		return err
	}
	return s.startProc()
}

// WriteConfig atomically writes config.json WITHOUT validating or restarting
// Xray. Used after live user add/remove so a crash-restart (the monitor reloads
// from disk) preserves the change. The content is generated by trusted code, so
// validation (which would reparse the geo DBs and cost seconds) is skipped.
func (s *Supervisor) WriteConfig(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0o700); err != nil {
		return err
	}
	tmp := s.configPath + ".new"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	// appliedCfg is deliberately NOT updated: the running process still has the old
	// config, and that difference is what tells the next Apply it must restart.
	return os.Rename(tmp, s.configPath)
}

// AddUsers adds users to the running Xray's inbounds via `xray api adu` (no
// restart). inbounds carry the tag + protocol + clients to add.
// runXray runs `xray <args...>` with the panel's env and the given timeout,
// returning the command's stdout. On failure the error wraps any stderr so callers
// get the diagnostic. Keeping stdout separate (vs CombinedOutput) means `api
// statsquery`'s JSON isn't polluted by stderr warnings.
func (s *Supervisor) runXray(timeout time.Duration, args ...string) ([]byte, error) {
	if s.bin == "" {
		return nil, fmt.Errorf("xray binary unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.bin, args...)
	cmd.Env = s.env()
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(errBuf.String()); msg != "" {
			return out, fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}

func (s *Supervisor) AddUsers(apiAddr string, inbounds []Inbound) error {
	if s.bin == "" {
		return fmt.Errorf("xray binary unavailable")
	}
	if len(inbounds) == 0 {
		return nil
	}
	data, err := json.Marshal(map[string]any{"inbounds": inbounds})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "xray-adu-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	f.Close()

	out, err := s.runXray(statsTimeout, "api", "adu", "--server="+apiAddr, f.Name())
	if err != nil {
		return fmt.Errorf("api adu: %w", err)
	}
	// `xray api adu` exits 0 even when it adds nobody: a rejected inbound prints its
	// reason and the run still ends "Added N user(s) in total". Comparing N against
	// what we asked for is the only reliable signal — matching on error text would
	// miss a PARTIAL failure, where some inbounds took their users and one did not
	// (that run still reports a non-zero N). A user silently missing from one lane
	// until something unrelated regenerates the config is exactly what this catches.
	if want := countInboundUsers(inbounds); reportedAdded(out) != want {
		return fmt.Errorf("api adu added %d of %d user entries: %s",
			reportedAdded(out), want, bytes.TrimSpace(out))
	}
	return nil
}

// addedRe pulls N out of xray's closing "Added N user(s) in total." line.
var addedRe = regexp.MustCompile(`Added (\d+) user\(s\)`)

// reportedAdded is how many user entries xray says it added, or -1 when its output
// carries no such line (an output shape we don't recognise is not a success).
func reportedAdded(out []byte) int {
	m := addedRe.FindSubmatch(out)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return -1
	}
	return n
}

// countInboundUsers totals the user entries across inbounds — the number xray should
// report back. Settings is an interface, so this enumerates the shapes AddUsers is
// ever handed; an unrecognised one contributes nothing rather than guessing.
func countInboundUsers(inbounds []Inbound) int {
	n := 0
	for _, in := range inbounds {
		switch s := in.Settings.(type) {
		case VLESSInboundSettings:
			n += len(s.Clients)
		case TrojanInboundSettings:
			n += len(s.Clients)
		case HysteriaInboundSettings:
			n += len(s.Users)
		}
	}
	return n
}

// ReplaceInbounds rebuilds whole inbounds in the running Xray: each one is removed
// and re-added through the API, which reconstructs it from scratch — users included.
//
// This exists for Hysteria2. Xray's HandlerService cannot manage users on a QUIC
// inbound: `adu` refuses outright ("unsupported inbound type") and, worse, `rmu`
// reports success while doing nothing, so a revoked user keeps their access. The
// only way to change that user set was a full Xray restart, which drops every OTHER
// lane's connections and the panel's own (:443 is Xray's; the panel sits on its
// fallback) for a change that concerns one inbound.
//
// Verified against Xray 26.7.28: rmi + adi swap the user set with the process
// untouched — same pid, same listening socket.
//
// Failure is the caller's to handle: rmi may have already landed, leaving that lane
// down, so a caller that cannot fix it must fall back to a full reconcile.
func (s *Supervisor) ReplaceInbounds(apiAddr string, inbounds []Inbound) error {
	if s.bin == "" {
		return fmt.Errorf("xray binary unavailable")
	}
	for _, in := range inbounds {
		if in.Tag == "" {
			return fmt.Errorf("inbound with no tag")
		}
		data, err := json.Marshal(map[string]any{"inbounds": []Inbound{in}})
		if err != nil {
			return err
		}
		f, err := os.CreateTemp("", "xray-adi-*.json")
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(f.Name())
			return err
		}
		f.Close()

		// A failed removal is not fatal on its own — an inbound that isn't there is
		// exactly the state the add below wants. Only the add has to succeed.
		if _, err := s.runXray(statsTimeout, "api", "rmi", "--server="+apiAddr, in.Tag); err != nil {
			slog.Warn("xray: could not remove inbound before re-adding it", "tag", in.Tag, "err", err)
		}
		out, err := s.runXray(statsTimeout, "api", "adi", "--server="+apiAddr, f.Name())
		os.Remove(f.Name())
		if err != nil {
			return fmt.Errorf("api adi tag=%s: %w", in.Tag, err)
		}
		// The CLI exits 0 even when it added nothing, so the output is the only
		// evidence. Leaving that unchecked is how a lane could quietly stay down.
		if bytes.Contains(out, []byte("failed to")) {
			return fmt.Errorf("api adi tag=%s: %s", in.Tag, bytes.TrimSpace(out))
		}
	}
	return nil
}

// RemoveUsers removes users (by email) from each given inbound tag via
// `xray api rmu` (no restart).
func (s *Supervisor) RemoveUsers(apiAddr string, tags, emails []string) error {
	if len(emails) == 0 || len(tags) == 0 {
		return nil
	}
	for _, tag := range tags {
		args := append([]string{"api", "rmu", "--server=" + apiAddr, "-tag=" + tag}, emails...)
		if _, err := s.runXray(statsTimeout, args...); err != nil {
			return fmt.Errorf("api rmu tag=%s: %w", tag, err)
		}
	}
	return nil
}

// restart stops the current process (if any) and starts a fresh one from the
// on-disk config. Caller must hold s.runMu.
func (s *Supervisor) restart() error {
	if s.bin == "" {
		slog.Info("xray: config written (no binary; not started)", "path", s.configPath)
		return nil
	}
	// Mark the bounce so Serving() covers the sub-second gap between the kill and
	// the new process — a deliberate restart is not an outage. Cleared unconditionally
	// so a failed start still ends the grace and lets the real "down" show.
	s.mu.Lock()
	s.restarting = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.restarting = false
		s.mu.Unlock()
	}()
	s.stopProc()
	return s.startProc()
}

// startProc launches Xray and a monitor goroutine that reaps it and triggers an
// auto-restart on unexpected exit. Caller must hold s.runMu.
func (s *Supervisor) startProc() error {
	s.mu.Lock()
	closed, suspended := s.closed, s.suspended
	s.mu.Unlock()
	if closed || suspended {
		// Not an error: both are states we deliberately put ourselves in. But it is
		// worth a line either way — a start that quietly does nothing and reports
		// success is how a dead Xray hid behind an operator's restart button
		// for hours.
		reason := "the supervisor is shut down"
		if suspended {
			reason = "the server is switched off (suspended)"
		}
		slog.Warn("xray: start ignored — "+reason, "config", s.configPath)
		return nil
	}
	cmd := exec.Command(s.bin, "run", "-c", s.configPath)
	cmd.Env = s.env()
	// Tap both streams: parse access logs from stdout, buffer/broadcast every line
	// (stdout + stderr) for the dashboard log viewer, and forward to journald.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("xray stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("xray stderr pipe: %w", err)
	}
	// Snapshot the config BEFORE handing it to Xray, not after. WriteConfig rewrites
	// this file on every user sync, and reading it afterwards can catch a revision
	// that landed in between — which would promote, as "the config that ran", one the
	// process never read. Read first and the race falls the safe way instead: at worst
	// a revision behind, and that one was proven when it ran.
	startedWith, _ := os.ReadFile(s.configPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start xray: %w", err)
	}
	p := &proc{cmd: cmd, done: make(chan struct{}), started: time.Now(), cfg: startedWith}
	s.mu.Lock()
	s.cur = p
	s.mu.Unlock()
	go s.tap(stdout, os.Stdout, true)
	go s.tap(stderr, os.Stderr, false)
	go s.monitor(p)
	go s.promoteWhenHealthy(p)
	slog.Info("xray: started", "pid", cmd.Process.Pid, "config", s.configPath)
	return nil
}

// stopProc kills the current process and blocks until its monitor has reaped it
// (so :443 is free before a replacement binds). It marks the proc as an
// intentional stop so the monitor won't auto-restart it. Caller must hold
// s.runMu; only the short state section takes s.mu.
func (s *Supervisor) stopProc() {
	s.mu.Lock()
	p := s.cur
	if p == nil || p.cmd.Process == nil {
		s.mu.Unlock()
		return
	}
	p.stop = true
	s.cur = nil
	s.restarts = 0
	s.mu.Unlock()

	_ = p.cmd.Process.Kill()
	<-p.done // monitor's Wait() returned → process fully reaped
}

// monitor waits for p to exit. An intentional stop (or a process already
// superseded by a newer one) is left alone; anything else is treated as a crash
// and auto-restarted with exponential backoff.
func (s *Supervisor) monitor(p *proc) {
	err := p.cmd.Wait()
	close(p.done) // unblocks stopProc() waiting to reuse :443

	s.mu.Lock()
	// s.closed matters as much as p.stop here. systemd's default KillMode signals
	// every process in the cgroup, so on `systemctl stop` Xray gets its own SIGTERM
	// and can die before the panel's handler reaches Stop() — p.stop is still false,
	// and the exit reads as a crash. That produced an "Xray crashed" alert
	// on every ordinary restart, with no all-clear after it: the restart is scheduled
	// a second later, by which time the panel itself is gone.
	if p.stop || s.cur != p || s.closed {
		s.mu.Unlock()
		return // intentional kill, already replaced, or the panel is shutting down
	}
	s.cur = nil
	// A run that reached healthy uptime is proven-good config: reset the backoff,
	// and don't let it qualify for auto-rollback — only an *immediate* crash after
	// a config change should roll back a known-good previous config.
	quickCrash := time.Since(p.started) <= healthyUptime
	if !quickCrash {
		s.restarts = 0 // it ran fine for a while; treat this as a fresh failure
	}
	s.mu.Unlock()

	slog.Warn("xray: exited unexpectedly, supervising restart", "err", err)
	if s.onCrash != nil {
		go s.onCrash(err) // off the monitor path so a slow notifier can't delay restart
	}
	s.superviseRestart(quickCrash)
}

// superviseRestart keeps trying to bring Xray back up with exponential backoff
// until it succeeds, is superseded by an Apply, or the panel shuts down. It
// takes s.runMu only around the actual start, never while sleeping, so an Apply
// can preempt it.
func (s *Supervisor) superviseRestart(quickCrash bool) {
	// Two reasons to roll back, and a run that reached healthy uptime is neither: a
	// proven-good config is never reverted, which is what quickCrash gates.
	//
	// The first is the original one — an immediate crash right after an Apply. It
	// covers what -test cannot see, a config that loads but will not run: a port
	// something else already holds, say.
	//
	// The second exists because Apply is not the only way config.json changes.
	// WriteConfig moves the file ahead of the running process on every user sync and
	// deliberately skips validation (it would cost 7-8s on the hot path), so an
	// unloadable config can sit on disk for hours while Xray keeps serving from
	// memory — and then detonate at the next restart, triggered by something entirely
	// unrelated like a certificate renewal. lastApply is untouched by that path, so
	// the first rule sleeps through exactly the case that leaves the tunnels down
	// until somebody logs in. Asking Xray whether the file is loadable is a
	// definitive answer where crash-shape heuristics are a guess, and the seven
	// seconds it costs are seconds we are already down for.
	s.mu.Lock()
	firstCrash := s.restarts == 0
	recentApply := !s.lastApply.IsZero() && time.Since(s.lastApply) < 2*healthyUptime
	rolledBack := s.rolledBack
	s.mu.Unlock()
	if shouldRollback(quickCrash, s.HasBackup(), firstCrash, recentApply, rolledBack,
		s.currentConfigUnloadable) {
		slog.Warn("xray: crashed after config change, attempting auto-rollback")
		s.runMu.Lock()
		s.mu.Lock()
		skip := s.closed || s.suspended || s.cur != nil
		s.mu.Unlock()
		if !skip {
			s.mu.Lock()
			s.rolledBack = true
			s.mu.Unlock()
			if err := s.restoreBackupLocked(); err == nil {
				slog.Info("xray: auto-rollback succeeded")
				s.mu.Lock()
				reason := s.lastLoadErr
				s.lastLoadErr = ""
				s.mu.Unlock()
				if s.onRolledBack != nil && reason != "" {
					go s.onRolledBack(reason)
				}
				s.runMu.Unlock()
				s.recovered()
				return
			} else {
				slog.Error("xray: auto-rollback failed", "err", err)
			}
		}
		s.runMu.Unlock()
	}

	for {
		s.mu.Lock()
		if s.closed || s.suspended || s.cur != nil {
			s.mu.Unlock()
			return // shutting down, suspended, or an Apply already started a new one
		}
		delay := backoffFor(s.restarts)
		s.restarts++
		s.mu.Unlock()

		slog.Info("xray: restarting", "delay", delay)
		time.Sleep(delay)

		s.runMu.Lock()
		s.mu.Lock()
		skip := s.closed || s.suspended || s.cur != nil
		s.mu.Unlock()
		if skip {
			s.runMu.Unlock()
			return
		}
		err := s.startProc()
		s.runMu.Unlock()
		if err == nil {
			s.recovered()
			return // the new process's monitor takes over
		}
		slog.Error("xray: restart failed", "err", err)
	}
}

// backoffFor returns the crash-restart delay for the nth consecutive attempt:
// base, 2×, 4×, … capped at maxBackoff.
func backoffFor(n int) time.Duration {
	if n >= 5 {
		return maxBackoff
	}
	d := restartBackoff << n
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// tap reads one Xray output stream line-by-line: it forwards each line to w (so
// journald keeps the full log), records it in the log hub for the dashboard
// viewer, and — when access is set — extracts connection info from access lines.
func (s *Supervisor) tap(r io.Reader, w io.Writer, access bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Drop the panel's own stats-API polling noise ("[api -> api]") — it would
		// otherwise flood both journald and the log viewer every few seconds.
		if strings.Contains(line, "[api ") {
			continue
		}
		fmt.Fprintln(w, line)
		fmt.Fprintln(s.logs, line)
		if access && s.onAccess != nil {
			if email, ip, dest := parseAccess(line); email != "" && ip != "" {
				s.dispatchAccess(email, ip, dest)
			}
		}
	}
}

// dispatchAccess invokes the onAccess callback, recovering from any panic so a
// malformed line or a store hiccup can't tear down the access-log reader.
func (s *Supervisor) dispatchAccess(email, ip, dest string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("xray: onAccess panic recovered", "panic", r)
		}
	}()
	s.onAccess(email, ip, dest)
}

// parseAccess pulls the user email, source IP and destination host out of an Xray
// access line:
//
//	... from 1.2.3.4:5678 accepted tcp:host:443 [in >> out] email: u1
//
// Loopback sources (the Trojan-WS fallback hop) are ignored.
//
// dest is best-effort and may be empty even when email and ip are set: only
// "accepted" lines carry a destination, and a line whose destination does not look
// like a host is treated as having none. It must never cost us the sighting — the
// device cap runs off email+ip alone and predates this.
//
// Because every user-facing inbound enables sniffing (see generate.go), dest is the
// real SNI/Host for TLS, HTTP and QUIC rather than the dialled IP.
func parseAccess(line string) (email, ip, dest string) {
	e := strings.Index(line, "email: ")
	if e < 0 {
		return "", "", ""
	}
	email = strings.TrimSpace(line[e+len("email: "):])

	f := strings.Index(line, "from ")
	if f < 0 {
		return "", "", ""
	}
	rest := line[f+len("from "):]
	if sp := strings.IndexByte(rest, ' '); sp > 0 {
		rest = rest[:sp]
	}
	host := hostOf(rest)
	if host == "" || host == "127.0.0.1" || host == "::1" {
		return "", "", ""
	}

	// Leading space so the marker cannot match inside some other token. Anything that
	// does not parse as a host is dropped: the segment after "accepted" is only a
	// destination on real connection lines, and on anything else (a truncated line, a
	// future Xray format) it is arbitrary text we must not report as a domain.
	if a := strings.Index(line, " accepted "); a >= 0 {
		d := line[a+len(" accepted "):]
		if sp := strings.IndexByte(d, ' '); sp > 0 {
			d = d[:sp]
		}
		// Normalise before validating so the two agree on what the host is: a trailing
		// root dot and upper-case SNI would otherwise each split one domain into two
		// buckets downstream.
		h := strings.ToLower(strings.TrimSuffix(hostOf(d), "."))
		if validHost(h) {
			dest = h
		}
	}
	return email, host, dest
}

// hostOf strips the optional network prefix and the port off an Xray address token
// ("tcp:1.2.3.4:5678", "udp:[2001:db8::1]:53", "example.com:443"), returning the
// bare host. Tokens without a port pass through unchanged.
func hostOf(s string) string {
	s = strings.TrimPrefix(s, "tcp:")
	s = strings.TrimPrefix(s, "udp:")
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}

// validHost reports whether s is plausibly a destination host — an IP literal, or a
// dotted name made only of label characters.
//
// This exists because SplitHostPort is happy to call almost anything a host: the
// tail of "... accepted email: u3" parses as host "email", and without this check
// that lands in the analytics as a visited domain.
func validHost(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	if !strings.Contains(s, ".") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") ||
		strings.Contains(s, "..") {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

// Stop terminates the Xray process and permanently closes the supervisor: every
// later Apply/Restart is silently ignored. It is for process shutdown only — use
// Suspend for a stop you intend to come back from.
func (s *Supervisor) Stop() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stopProc()
}

// Suspend terminates the Xray process but leaves the supervisor usable, so a later
// Apply or Restart brings it back. The auto-restart never fires for it: stopProc
// marks the process as killed on purpose, which is what the monitor keys on — the
// closed latch is not needed for that and is exactly what makes Stop one-way.
//
// This exists because a node stops serving for two very different reasons. One is
// the agent going away; the other is the panel revoking it (disabled or deleted),
// which is reversible — re-enable it and it must serve again. Using Stop for the
// second poisoned the supervisor for good: the node kept syncing, applied every
// config it was pushed, reported each operator restart as a success, and never ran
// Xray again until its process was restarted by hand.
func (s *Supervisor) Suspend() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	s.suspended = true
	s.mu.Unlock()
	s.stopProc()
}

// Resume lifts a Suspend and starts Xray from the config on disk.
//
// Lifting the suspension is deliberately NOT a side effect of Apply or Restart.
// A suspended node is one the panel switched off, and it still does everything
// else — renews its certificate, accepts a config, keeps checking in — so any of
// those paths would quietly put it back to serving users. Coming back has to be
// something the caller asks for by name, in the one place that knows the panel
// switched it on again.
func (s *Supervisor) Resume() error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	s.suspended = false
	s.mu.Unlock()
	return s.restart()
}

// Traffic is a per-user uplink/downlink counter snapshot.
type Traffic struct {
	Up   int64
	Down int64
}

// QueryStats reads per-user traffic counters from the running Xray StatsService
// (via `xray api statsquery`). Keyed by user email (we use "u<id>").
func (s *Supervisor) QueryStats(apiAddr string) (map[string]Traffic, error) {
	// Timeout so a wedged API port can't hang the stats poller forever.
	out, err := s.runXray(statsTimeout, "api", "statsquery", "--server="+apiAddr, "user>>>")
	if err != nil {
		return nil, fmt.Errorf("statsquery: %w", err)
	}
	return parseStats(out), nil
}

// parseStats turns the StatsService JSON into per-email Traffic. Stat names look
// like "user>>>u1>>>traffic>>>uplink".
func parseStats(data []byte) map[string]Traffic {
	var resp struct {
		Stat []struct {
			Name string `json:"name"`
			// Xray emits value as a JSON number (26.x) but older builds used a
			// string — RawMessage + quote-trim accepts both.
			Value json.RawMessage `json:"value"`
		} `json:"stat"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil
	}
	out := map[string]Traffic{}
	for _, st := range resp.Stat {
		parts := strings.Split(st.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		email, dir := parts[1], parts[3]
		val, _ := strconv.ParseInt(strings.Trim(string(st.Value), `"`), 10, 64)
		t := out[email]
		switch dir {
		case "uplink":
			t.Up = val
		case "downlink":
			t.Down = val
		}
		out[email] = t
	}
	return out
}

// shouldRollback decides whether to revert config.json to its backup after Xray went
// down. Split out because the rule has five inputs and one of them costs seven seconds
// to evaluate, so both what it decides and WHEN it bothers asking are worth pinning.
//
// unloadable is a function, not a bool, on purpose: it runs Xray over the config file
// and must not be called when the cheaper terms have already settled the question.
func shouldRollback(quickCrash, hasBackup, firstCrash, recentApply, rolledBack bool, unloadable func() bool) bool {
	// A run that reached healthy uptime proved its config; whatever killed it later is
	// not the config, and reverting would throw away real changes.
	if !quickCrash || !hasBackup {
		return false
	}
	// The original rule: crashed straight after an Apply. Catches what validation
	// cannot see — a config that loads but will not run, like a port already held.
	if firstCrash && recentApply {
		return true
	}
	// Once per generation, or a backup that is itself unusable would loop.
	return !rolledBack && unloadable()
}

// currentConfigUnloadable reports whether the config file on disk is one Xray will
// refuse to start with. Used only when Xray is already down, so the cost of asking is
// paid out of an outage rather than out of a healthy request path.
//
// Answers false when there is no binary to ask, or when the file cannot be read: this
// gates a rollback, and "we could not tell" must not be treated as "it is broken".
func (s *Supervisor) currentConfigUnloadable() bool {
	if s == nil || s.bin == "" {
		return false
	}
	data, err := os.ReadFile(s.configPath)
	if err != nil {
		return false
	}
	if err := s.ValidateBytes(data); err != nil {
		slog.Warn("xray: the config on disk does not load", "err", err)
		// Kept for the rollback that follows: the operator is about to have a change
		// reverted under them, and this line is the only thing that says why.
		s.mu.Lock()
		s.lastLoadErr = err.Error()
		s.mu.Unlock()
		return true
	}
	return false
}

// promoteWhenHealthy makes the rollback copy the last config that actually RAN, rather
// than the last one that was structurally applied.
//
// config.json.bak was only ever written by Apply, so it held whatever preceded the last
// structural change. Everything a user sync wrote afterwards — WriteConfig moves the
// file without touching the copy — was missing from it, which made the copy an
// arbitrarily old thing to fall back to. That mattered little while a rollback could
// only follow an Apply; it matters now that any config the file cannot load reaches for
// it.
//
// Waits out healthyUptime because a config that crashes immediately must never become
// the thing we roll back TO. Promotion is best-effort and silent: failing to refresh the
// copy leaves the previous one, which is the conservative half of the trade.
// promoteAfter is how long a run must last before its config is trusted as the
// rollback target. A variable so tests need not wait it out; nothing else writes it.
var promoteAfter = healthyUptime

func (s *Supervisor) promoteWhenHealthy(p *proc) {
	if len(p.cfg) == 0 {
		return
	}
	select {
	case <-p.done: // died before proving anything
		return
	case <-time.After(promoteAfter):
	}
	s.mu.Lock()
	current := s.cur == p && !s.closed
	s.mu.Unlock()
	if !current {
		return
	}
	if err := os.WriteFile(s.configPath+".bak", p.cfg, 0o600); err != nil {
		slog.Warn("xray: could not refresh the rollback config", "err", err)
	}
}
