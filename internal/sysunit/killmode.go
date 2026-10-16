// Package sysunit adjusts the systemd unit the panel or a node runs under, for
// installs whose unit was written by an older version.
package sysunit

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// dropIn is the override that makes systemd signal the service's main process alone
// on stop. Under the default (control-group) Xray, a child, gets SIGTERM at the
// same moment and dies before the panel or node can read its traffic counters —
// up to a minute of everyone's traffic per restart.
const dropIn = "[Service]\nKillMode=mixed\n"

const dropInName = "10-rospanel-killmode.conf"

// EnsureKillModeMixed puts KillMode=mixed on the unit when the service runs under
// systemd with the default KillMode; it takes effect from the next stop.
// Units written by this version carry it already. Best effort: a box where it
// cannot be written keeps today's behaviour.
func EnsureKillModeMixed(unit string) {
	if runtime.GOOS != "linux" || os.Getenv("INVOCATION_ID") == "" {
		return // not started by systemd
	}
	ensure(unit, "/etc/systemd/system", run)
}

// ensure is EnsureKillModeMixed over a unit directory and a command runner, for
// tests.
func ensure(unit, dir string, cmd func(args ...string) (string, error)) bool {
	if _, err := os.Stat(filepath.Join(dir, unit)); err != nil {
		return false // not our unit file (a package, a container): leave it alone
	}
	// Only the default is replaced: mixed is what we want, and any other mode is an
	// operator's own choice.
	if mode, err := cmd("show", unit, "-p", "KillMode", "--value"); err != nil || strings.TrimSpace(mode) != "control-group" {
		return false
	}
	d := filepath.Join(dir, unit+".d")
	if err := os.MkdirAll(d, 0o755); err != nil {
		slog.Warn("systemd: cannot add KillMode=mixed", "unit", unit, "err", err)
		return false
	}
	if err := os.WriteFile(filepath.Join(d, dropInName), []byte(dropIn), 0o644); err != nil {
		slog.Warn("systemd: cannot add KillMode=mixed", "unit", unit, "err", err)
		return false
	}
	if _, err := cmd("daemon-reload"); err != nil {
		slog.Warn("systemd: daemon-reload after adding KillMode=mixed failed", "unit", unit, "err", err)
		return false
	}
	slog.Info("systemd: KillMode=mixed set — Xray's traffic is read before it stops", "unit", unit)
	return true
}

func run(args ...string) (string, error) {
	out, err := exec.Command("systemctl", args...).Output()
	return string(out), err
}

// RemoveDropIn deletes the override, for an uninstall that removes the unit.
func RemoveDropIn(unitPath string) {
	d := unitPath + ".d"
	_ = os.Remove(filepath.Join(d, dropInName))
	_ = os.Remove(d) // only if nothing else is in it
}
