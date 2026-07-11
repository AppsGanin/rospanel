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
	"strconv"
	"strings"
	"sync"
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
)

// proc is a single running Xray child. done is closed once Wait() has reaped it;
// stop marks an intentional kill so the monitor won't restart it.
type proc struct {
	cmd     *exec.Cmd
	done    chan struct{}
	started time.Time
	stop    bool
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
	closed    bool       // panel is shutting down; do not (re)start
	restarts  int        // consecutive crash restarts (backoff exponent)
	lastApply time.Time  // when the last Apply() succeeded (zero if never)

	onAccess func(email, ip string) // called per access-log connection line
	onCrash  func(err error)        // called when Xray exits unexpectedly (crash)

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
func (s *Supervisor) SetOnAccess(fn func(email, ip string)) { s.onAccess = fn }

// SetOnCrash registers a callback invoked when the Xray child exits unexpectedly
// (a genuine crash, not an intentional Stop/Apply). Used to alert the operator.
func (s *Supervisor) SetOnCrash(fn func(err error)) { s.onCrash = fn }

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

// Running reports whether the Xray child process is currently up. Reflects
// reality: a crashed process clears s.cur until a restart succeeds.
func (s *Supervisor) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur != nil
}

// Apply writes the config atomically (validating first when possible) and
// restarts Xray.
func (s *Supervisor) Apply(cfg *Config) error {
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

	if _, err := s.runXray(statsTimeout, "api", "adu", "--server="+apiAddr, f.Name()); err != nil {
		return fmt.Errorf("api adu: %w", err)
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
	s.stopProc()
	return s.startProc()
}

// startProc launches Xray and a monitor goroutine that reaps it and triggers an
// auto-restart on unexpected exit. Caller must hold s.runMu.
func (s *Supervisor) startProc() error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
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
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start xray: %w", err)
	}
	p := &proc{cmd: cmd, done: make(chan struct{}), started: time.Now()}
	s.mu.Lock()
	s.cur = p
	s.mu.Unlock()
	go s.tap(stdout, os.Stdout, true)
	go s.tap(stderr, os.Stderr, false)
	go s.monitor(p)
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
	if p.stop || s.cur != p {
		s.mu.Unlock()
		return // intentional kill or already replaced
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
	// Auto-roll back only when Xray crashed *immediately* after a recent Apply (it
	// never reached healthy uptime) and a backup exists. A healthy-then-crashed run
	// passes quickCrash=false so a proven-good config is never reverted.
	s.mu.Lock()
	firstCrash := s.restarts == 0
	recentApply := !s.lastApply.IsZero() && time.Since(s.lastApply) < 2*healthyUptime
	s.mu.Unlock()
	if quickCrash && firstCrash && recentApply && s.HasBackup() {
		slog.Warn("xray: crashed after config change, attempting auto-rollback")
		s.runMu.Lock()
		s.mu.Lock()
		skip := s.closed || s.cur != nil
		s.mu.Unlock()
		if !skip {
			if err := s.restoreBackupLocked(); err == nil {
				slog.Info("xray: auto-rollback succeeded")
				s.runMu.Unlock()
				return
			} else {
				slog.Error("xray: auto-rollback failed", "err", err)
			}
		}
		s.runMu.Unlock()
	}

	for {
		s.mu.Lock()
		if s.closed || s.cur != nil {
			s.mu.Unlock()
			return // shutting down, or an Apply already started a new process
		}
		delay := backoffFor(s.restarts)
		s.restarts++
		s.mu.Unlock()

		slog.Info("xray: restarting", "delay", delay)
		time.Sleep(delay)

		s.runMu.Lock()
		s.mu.Lock()
		skip := s.closed || s.cur != nil
		s.mu.Unlock()
		if skip {
			s.runMu.Unlock()
			return
		}
		err := s.startProc()
		s.runMu.Unlock()
		if err == nil {
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
			if email, ip := parseAccess(line); email != "" && ip != "" {
				s.dispatchAccess(email, ip)
			}
		}
	}
}

// dispatchAccess invokes the onAccess callback, recovering from any panic so a
// malformed line or a store hiccup can't tear down the access-log reader.
func (s *Supervisor) dispatchAccess(email, ip string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("xray: onAccess panic recovered", "panic", r)
		}
	}()
	s.onAccess(email, ip)
}

// parseAccess pulls the user email and source IP out of an Xray access line:
//
//	... from 1.2.3.4:5678 accepted tcp:host:443 [in >> out] email: u1
//
// Loopback sources (the Trojan-WS fallback hop) are ignored.
func parseAccess(line string) (email, ip string) {
	e := strings.Index(line, "email: ")
	if e < 0 {
		return "", ""
	}
	email = strings.TrimSpace(line[e+len("email: "):])

	f := strings.Index(line, "from ")
	if f < 0 {
		return "", ""
	}
	rest := line[f+len("from "):]
	if sp := strings.IndexByte(rest, ' '); sp > 0 {
		rest = rest[:sp]
	}
	// Some lines prefix the source with the network ("tcp:1.2.3.4:5678"); strip it
	// so SplitHostPort sees a plain host:port and we key connections by IP only.
	rest = strings.TrimPrefix(rest, "tcp:")
	rest = strings.TrimPrefix(rest, "udp:")
	host := rest
	if h, _, err := net.SplitHostPort(rest); err == nil {
		host = h
	}
	if host == "" || host == "127.0.0.1" || host == "::1" {
		return "", ""
	}
	return email, host
}

// Stop terminates the Xray process and prevents further (re)starts (used on
// panel shutdown).
func (s *Supervisor) Stop() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stopProc()
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
