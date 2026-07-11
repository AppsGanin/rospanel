// Package tuning applies host-level network optimizations recommended for an
// Xray server — currently enabling the TCP BBR congestion-control algorithm,
// which noticeably improves throughput on lossy/long-haul links.
//
// See https://xtls.github.io/en/document/level-0/ch07-xray-server.html (7.7).
package tuning

import (
	"os"
	"os/exec"
	"strings"
)

const (
	ccPath       = "/proc/sys/net/ipv4/tcp_congestion_control"
	qdiscPath    = "/proc/sys/net/core/default_qdisc"
	sysctlDropin = "/etc/sysctl.d/99-rospanel-bbr.conf"
	dropinBody   = "# Managed by RosPanel — TCP BBR congestion control.\n" +
		"net.core.default_qdisc=fq\n" +
		"net.ipv4.tcp_congestion_control=bbr\n"
)

// BBRState is the outcome of EnsureBBR, for logging.
type BBRState int

const (
	BBRAlready   BBRState = iota // already active, nothing to do
	BBREnabled                   // we just switched it on
	BBRUnchanged                 // couldn't enable (kernel lacks BBR, not root, …)
)

// EnsureBBR enables BBR congestion control if it isn't already active. It persists
// the setting via a sysctl drop-in (so it survives reboots) AND applies it at
// runtime, so no reboot is needed. Best-effort and idempotent: it returns the
// resulting state plus any error for the caller to log, and never fails the boot.
// Requires root (the panel runs as root under systemd); on a non-Linux/non-root
// host the /proc and /etc writes simply fail and it reports BBRUnchanged.
func EnsureBBR() (BBRState, error) {
	if cur, _ := readTrim(ccPath); cur == "bbr" {
		return BBRAlready, nil
	}
	// BBR may live in a module; harmless if it's built-in or already loaded.
	_ = exec.Command("modprobe", "tcp_bbr").Run()

	// Persist for future boots (don't abort runtime apply if this fails).
	werr := os.WriteFile(sysctlDropin, []byte(dropinBody), 0o644)

	// Apply now, directly via /proc (no dependency on the `sysctl` binary).
	_ = os.WriteFile(qdiscPath, []byte("fq\n"), 0o644)
	if err := os.WriteFile(ccPath, []byte("bbr\n"), 0o644); err != nil {
		if werr == nil {
			werr = err
		}
	}

	if cur, _ := readTrim(ccPath); cur == "bbr" {
		return BBREnabled, nil
	}
	return BBRUnchanged, werr
}

// Active reports whether BBR is the congestion-control algorithm in force right
// now. It re-reads the kernel rather than trusting what EnsureBBR returned at boot,
// so the health report reflects reality even if something changed the sysctl since.
func Active() bool {
	cur, err := readTrim(ccPath)
	return err == nil && cur == "bbr"
}

func readTrim(p string) (string, error) {
	b, err := os.ReadFile(p)
	return strings.TrimSpace(string(b)), err
}
