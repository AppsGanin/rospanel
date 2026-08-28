// Package firewall manages host-level firewall (UFW) configuration for RosPanel
// and VPN nodes. It ensures UFW is installed, configures safe default policies
// (deny incoming, allow outgoing), protects the SSH port from lockouts, and opens
// all required application ports and port ranges.
package firewall

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rule represents a port or port range rule for the firewall.
type Rule struct {
	PortStart int    `json:"port_start"`
	PortEnd   int    `json:"port_end,omitempty"` // 0 if single port
	Proto     string `json:"proto"`              // "tcp", "udp", or "" (both)
	Comment   string `json:"comment,omitempty"`
}

// Normalize validates and normalizes the rule parameters.
func (r Rule) Normalize() (Rule, bool) {
	if r.PortStart < 1 || r.PortStart > 65535 {
		return r, false
	}
	r.Proto = strings.ToLower(strings.TrimSpace(r.Proto))
	if r.Proto != "" && r.Proto != "tcp" && r.Proto != "udp" {
		return r, false
	}
	if r.PortEnd > 0 {
		if r.PortEnd < r.PortStart || r.PortEnd > 65535 {
			return r, false
		}
		if r.PortEnd == r.PortStart {
			r.PortEnd = 0
		}
	}
	return r, true
}

// Format returns the UFW rule specification (e.g. "443/tcp", "20000:30000/udp", or "80").
func (r Rule) Format() string {
	proto := r.Proto
	if r.PortEnd > r.PortStart {
		if proto != "" {
			return fmt.Sprintf("%d:%d/%s", r.PortStart, r.PortEnd, proto)
		}
		return fmt.Sprintf("%d:%d", r.PortStart, r.PortEnd)
	}
	if proto != "" {
		return fmt.Sprintf("%d/%s", r.PortStart, proto)
	}
	return strconv.Itoa(r.PortStart)
}

var mu sync.Mutex

// Available reports whether UFW is installed and usable on this Linux host.
func Available() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := exec.LookPath("ufw")
	return err == nil
}

// IsActive checks if UFW is currently active.
func IsActive(ctx context.Context) bool {
	if !Available() {
		return false
	}
	out, err := exec.CommandContext(ctx, "ufw", "status").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Status: active")
}

// DetectSSHPorts discovers all active SSH ports on this host to guarantee
// the administrator is never locked out when activating the firewall.
// It inspects sshd configurations, SSH environment variables, and always includes port 22.
func DetectSSHPorts() []int {
	seen := map[int]bool{22: true}
	var ports []int

	// 1. Inspect /etc/ssh/sshd_config
	if b, err := os.ReadFile("/etc/ssh/sshd_config"); err == nil {
		for _, p := range parseSSHDConfigPorts(string(b)) {
			seen[p] = true
		}
	}

	// 2. Inspect /etc/ssh/sshd_config.d/*.conf
	if matches, err := filepath.Glob("/etc/ssh/sshd_config.d/*.conf"); err == nil {
		for _, match := range matches {
			if b, err := os.ReadFile(match); err == nil {
				for _, p := range parseSSHDConfigPorts(string(b)) {
					seen[p] = true
				}
			}
		}
	}

	// 3. Inspect SSH_CONNECTION or SSH_CLIENT environment variables
	for _, envKey := range []string{"SSH_CONNECTION", "SSH_CLIENT"} {
		if val := strings.TrimSpace(os.Getenv(envKey)); val != "" {
			fields := strings.Fields(val)
			if len(fields) >= 4 {
				if p, err := strconv.Atoi(fields[3]); err == nil && p >= 1 && p <= 65535 {
					seen[p] = true
				}
			}
		}
	}

	for p := range seen {
		ports = append(ports, p)
	}
	return ports
}

// parseSSHDConfigPorts extracts port numbers from sshd configuration text.
func parseSSHDConfigPorts(content string) []int {
	var ports []int
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.EqualFold(fields[0], "Port") {
			if p, err := strconv.Atoi(fields[1]); err == nil && p >= 1 && p <= 65535 {
				ports = append(ports, p)
			}
		}
	}
	return ports
}

// EnsureInstalled checks if ufw is installed and attempts to install it via the
// system package manager if missing. Non-fatal: logs and returns error if installation fails.
func EnsureInstalled(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if Available() {
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("ufw is not installed and cannot be installed without root privileges")
	}

	log.Printf("firewall: ufw not found — installing via system package manager…")

	// Detect available package manager
	var cmd *exec.Cmd
	switch {
	case fileExists("/usr/bin/apt-get") || fileExists("/bin/apt-get"):
		cmd = exec.CommandContext(ctx, "sh", "-c", "DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ufw")
	case fileExists("/usr/bin/dnf") || fileExists("/bin/dnf"):
		cmd = exec.CommandContext(ctx, "dnf", "install", "-y", "-q", "ufw")
	case fileExists("/usr/bin/yum") || fileExists("/bin/yum"):
		cmd = exec.CommandContext(ctx, "yum", "install", "-y", "-q", "ufw")
	case fileExists("/usr/bin/pacman") || fileExists("/bin/pacman"):
		cmd = exec.CommandContext(ctx, "pacman", "-Sy", "--noconfirm", "ufw")
	case fileExists("/sbin/apk") || fileExists("/usr/bin/apk"):
		cmd = exec.CommandContext(ctx, "apk", "add", "ufw")
	default:
		return fmt.Errorf("no supported package manager found to install ufw")
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install ufw: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	if !Available() {
		return fmt.Errorf("ufw package installed but ufw command is still not available in PATH")
	}

	log.Printf("firewall: ufw successfully installed")
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// EnsureActive ensures UFW is activated with secure default policies (default deny incoming,
// default allow outgoing) and that SSH ports are explicitly allowed before enabling.
func EnsureActive(ctx context.Context) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return nil
	}
	if !Available() {
		if err := EnsureInstalled(ctx); err != nil {
			return err
		}
	}

	// 1. Always ensure SSH ports are allowed first (anti-lockout)
	for _, p := range DetectSSHPorts() {
		_ = exec.CommandContext(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", p)).Run()
	}

	if IsActive(ctx) {
		return nil
	}

	log.Printf("firewall: activating UFW with secure default policies (deny incoming, allow outgoing)…")

	// 2. Set default policies
	if out, err := exec.CommandContext(ctx, "ufw", "default", "deny", "incoming").CombinedOutput(); err != nil {
		log.Printf("firewall: warning setting default deny incoming: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "ufw", "default", "allow", "outgoing").CombinedOutput(); err != nil {
		log.Printf("firewall: warning setting default allow outgoing: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	// 3. Re-verify SSH rule before enabling
	for _, p := range DetectSSHPorts() {
		_ = exec.CommandContext(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", p)).Run()
	}

	// 4. Force enable UFW
	if out, err := exec.CommandContext(ctx, "ufw", "--force", "enable").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to enable ufw: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	// Enable ufw service via systemctl if present
	if _, err := exec.LookPath("systemctl"); err == nil {
		_ = exec.CommandContext(ctx, "systemctl", "enable", "ufw").Run()
	}

	log.Printf("firewall: UFW activated successfully")
	return nil
}

// Allow opens a single rule in UFW.
func Allow(ctx context.Context, rule Rule) error {
	norm, ok := rule.Normalize()
	if !ok {
		return fmt.Errorf("invalid rule: %+v", rule)
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 || !Available() {
		return nil
	}

	spec := norm.Format()
	var args []string
	if norm.Comment != "" {
		args = []string{"allow", spec, "comment", norm.Comment}
	} else {
		args = []string{"allow", spec}
	}

	cmd := exec.CommandContext(ctx, "ufw", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		// If comment syntax fails on very old ufw versions, retry without comment
		if norm.Comment != "" {
			cmdRetry := exec.CommandContext(ctx, "ufw", "allow", spec)
			if outRetry, errRetry := cmdRetry.CombinedOutput(); errRetry == nil {
				return nil
			} else {
				return fmt.Errorf("ufw allow %s: %w (%s)", spec, errRetry, strings.TrimSpace(string(outRetry)))
			}
		}
		return fmt.Errorf("ufw allow %s: %w (%s)", spec, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// AllowMany opens a list of rules in UFW.
func AllowMany(ctx context.Context, rules []Rule) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 || !Available() {
		return nil
	}
	for _, r := range rules {
		if err := Allow(ctx, r); err != nil {
			log.Printf("firewall: allow %s: %v", r.Format(), err)
		}
	}
	return nil
}

// Sync performs a full firewall synchronization:
// 1. Ensures UFW is installed (installs if missing).
// 2. Ensures UFW is active with safe default policies and SSH allowed.
// 3. Ensures all required application rules are allowed.
// It is idempotent, thread-safe, and safe to call on startup and runtime updates.
func Sync(ctx context.Context, rules []Rule) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if os.Geteuid() != 0 {
		return nil
	}

	mu.Lock()
	defer mu.Unlock()

	// Bound overall sync execution time so it never hangs
	syncCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	if err := EnsureInstalled(syncCtx); err != nil {
		log.Printf("firewall: ensure installed: %v", err)
		return err
	}

	if err := EnsureActive(syncCtx); err != nil {
		log.Printf("firewall: ensure active: %v", err)
		return err
	}

	if err := AllowMany(syncCtx, rules); err != nil {
		log.Printf("firewall: allow rules: %v", err)
		return err
	}

	return nil
}

// TCPRule creates a standard single TCP port rule.
func TCPRule(port int, comment string) Rule {
	return Rule{PortStart: port, Proto: "tcp", Comment: comment}
}

// UDPRule creates a standard single UDP port rule.
func UDPRule(port int, comment string) Rule {
	return Rule{PortStart: port, Proto: "udp", Comment: comment}
}

// UDPRangeRule creates a UDP port range rule.
func UDPRangeRule(start, end int, comment string) Rule {
	return Rule{PortStart: start, PortEnd: end, Proto: "udp", Comment: comment}
}

// DeduplicateRules removes redundant or invalid rules.
func DeduplicateRules(rules []Rule) []Rule {
	var out []Rule
	seen := map[string]bool{}
	for _, r := range rules {
		norm, ok := r.Normalize()
		if !ok {
			continue
		}
		key := norm.Format()
		if !seen[key] {
			seen[key] = true
			out = append(out, norm)
		}
	}
	return out
}

// PortInUse returns whether a port is currently listening on loopback or any interface.
func PortInUse(network string, port int) bool {
	ln, err := net.Listen(network, fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}
