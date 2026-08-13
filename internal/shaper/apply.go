package shaper

import (
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Applier holds the last state put in force, so an unchanged one costs nothing and
// a state that becomes empty tears the tree down exactly once.
type Applier struct {
	mu        sync.Mutex
	lastHash  string
	installed bool
	lastWAN   string
	// started records that this process has run at least one pass. Until it has, the
	// kernel's tree may be one a PREVIOUS run left behind — a crash, a kill -9, an
	// upgrade that didn't stop cleanly — and that tree outlives the process. The
	// first pass therefore tears down unconditionally instead of assuming nothing is
	// installed; otherwise a panel that came back with no capped users would leave
	// yesterday's caps throttling people with nothing in the panel to explain it.
	started bool
}

// New returns an Applier with nothing installed.
func New() *Applier { return &Applier{} }

// Apply puts the desired state in force. It is a no-op when the state is unchanged,
// tears the tree down when nothing is shaped any more, and degrades to a logged
// warning wherever tc is unavailable.
func (a *Applier) Apply(st State) {
	if runtime.GOOS != "linux" {
		return
	}
	hash := Hash(st)
	a.mu.Lock()
	defer a.mu.Unlock()
	if hash == a.lastHash {
		return
	}

	cmds := Commands(st)
	if len(cmds) == 0 {
		// Nothing to shape. Tear down if we built something — or if this is the first
		// pass of the process, when what is installed may be a previous run's.
		// Otherwise an idle pass runs tc against an interface we never touched.
		if a.installed || !a.started {
			a.teardownLocked()
		}
		a.started = true
		a.lastHash = hash
		return
	}
	a.started = true
	if !hasTC() {
		slog.Warn("shaper: tc not available — per-user speed limits are NOT in force")
		a.lastHash = hash // don't retry every tick on a host that will never have it
		return
	}
	// Start from a clean tree: Commands assumes the classes and filters it adds are
	// the only ones there, and `filter add` accumulates rather than replaces.
	a.teardownLocked()

	var failed int
	for _, c := range cmds {
		if err := run(c); err != nil {
			// `ip link add ifb-rospanel` fails with "File exists" on every pass after
			// the first, which is expected and not worth a log line; the teardown above
			// removes it, so a surviving device means something else holds it.
			failed++
			slog.Debug("shaper: command failed", "cmd", strings.Join(c, " "), "err", err)
		}
	}
	if failed > 0 {
		slog.Warn("shaper: some commands failed — speed limits may be partial",
			"failed", failed, "total", len(cmds), "wan", st.WAN)
	} else {
		slog.Info("shaper: per-user speed limits applied",
			"users", len(shapeable(st.Rules)), "wan", st.WAN)
	}
	a.installed = true
	a.lastWAN = st.WAN
	a.lastHash = hash
}

// Reset drops everything this Applier installed. Called at shutdown paths that want
// the host left as they found it; the kernel would otherwise keep the tree until
// reboot.
func (a *Applier) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.teardownLocked()
	a.lastHash = ""
}

func (a *Applier) teardownLocked() {
	wan := a.lastWAN
	if wan == "" {
		wan = DefaultWAN()
	}
	for _, c := range TeardownCommands(wan) {
		_ = run(c) // every one of these fails when it was never installed
	}
	a.installed = false
}

func run(args []string) error {
	// No shell anywhere in here: arguments go to execve as they are, and the only
	// caller-influenced ones (addresses) are parsed before they get this far.
	return exec.Command(args[0], args[1:]...).Run()
}

func hasTC() bool {
	_, err := exec.LookPath("tc")
	return err == nil
}

// DefaultWAN returns the interface the default route leaves through — the one a
// user's traffic actually crosses. Empty when it can't be determined, which makes
// Apply a no-op rather than a guess at the wrong device.
func DefaultWAN() string {
	out, err := exec.Command("ip", "-o", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	return parseDefaultDev(string(out))
}

// parseDefaultDev picks the device out of `ip route show default` output. Split out
// so the parsing is testable without a host that has routes.
func parseDefaultDev(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// Only a default route names the interface we want. `ip -o route show default`
		// returns nothing else, but reading a device out of a subnet route — the shape
		// a caller passing full route output would hand us — would shape the wrong
		// interface, so require the prefix rather than trust the command line.
		if len(fields) == 0 || fields[0] != "default" {
			continue
		}
		for i, f := range fields {
			if f == "dev" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	return ""
}
