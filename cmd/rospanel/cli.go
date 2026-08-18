package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/backup"
	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/datasec"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/updater"
	"github.com/AppsGanin/rospanel/internal/version"
)

// printUsage writes the CLI help to w.
func printUsage(w io.Writer) {
	fmt.Fprint(w, `rospanel `+version.Version+` — VPN control panel (Xray + sing-box)

Usage:
  rospanel [command] [arguments]

With no command the panel server starts (normally launched by systemd).

Commands:
  install            Install and start the systemd service (root, Linux only).
  uninstall [-y]     Remove the systemd service (data in the data dir is kept).
  start              Start the service (systemctl start).
  stop               Stop the service (systemctl stop).
  restart            Restart the service (systemctl restart).
  status             Show the service status (systemctl status).
  update [-y]        Update to the latest GitHub release and restart.
  node <sub>         Node mode: install --join '<url>', run, set-panel, status,
                     uninstall (see rospanel node help).
  backup [file]      Create a .tar.gz backup (DB + certificates + Xray config).
                     Without an argument the file is named after the current time.
  restore [-y] <file>
                     Stage a restore from a backup; it is applied at the panel's
                     next start (restart the service).
  host [-y] [domain|IP]
                     Show the current address, or change the domain/IP (reissues TLS).
  path               Show the panel URL and check secrets.key / the database.
  totp reset <login> Remove an admin's two-factor authentication (lost phone).
                     Without arguments: list who has it enabled.
  rescue <sub>       Regain access when locked out: list, password <login>,
                     unlock <login> (password + clear 2FA), owner <login>.
  reset [-y]         Factory reset — wipes the entire database.
  version            Show the version.
  help               Show this help.

Flags:
  -y, --yes          Don't ask for confirmation
                     (for update, reset, uninstall, restore, host).

Examples:
  sudo rospanel install
  rospanel backup /root/rospanel.tar.gz
  rospanel restore /root/rospanel.tar.gz && systemctl restart rospanel
  rospanel host vpn.example.com
  rospanel update -y
`)
}

func runBackup(dataDir string, args []string) {
	out := fmt.Sprintf("rospanel-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
	if len(args) > 0 {
		out = args[0]
	}
	if err := backup.Create(dataDir, out); err != nil {
		log.Fatalf("backup failed: %v", err)
	}
	log.Printf("backup written: %s", out)
}

func runRestore(dataDir string, args []string) {
	src := firstPositional(args)
	if src == "" {
		log.Fatal("usage: rospanel restore [-y] <backup.tar.gz>")
	}
	if !hasYesFlag(args) && !confirmTTY(
		"Restoring a backup REPLACES all current data at the panel's next start:\n"+
			"users, settings, domain/TLS, the secret path. The current data will be\n"+
			"lost — take a backup first.\n"+
			"Continue? [y/N]: ") {
		fmt.Println("Cancelled.")
		return
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatalf("restore: %v", err)
	}
	// Same schema gate the upload path applies. Restoring an archive from a NEWER panel
	// leaves this binary reading columns its schema lacks — the migration runner skips
	// versions already recorded, so nothing runs — and the result is a boot loop with no
	// way out from inside the panel. Refusing here costs one message; not refusing costs
	// an SSH session and a newer binary.
	if err := checkRestoreSchema(src); err != nil {
		log.Fatalf("restore refused: %v", err)
	}
	if err := backup.StageRestore(src, dataDir); err != nil {
		log.Fatalf("restore failed: %v", err)
	}
	log.Printf("restore staged from %s — (re)start the panel to apply it", src)
}

// firstPositional returns the first non-flag argument (flags start with "-").
func firstPositional(args []string) string {
	for _, a := range args {
		if a != "" && !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// runPath prints the panel URL and checks secrets.key / database health.
func runPath(dataDir string) {
	if err := datasec.Init(dataDir); err != nil {
		fmt.Fprintln(os.Stderr, "secrets.key error:", err)
		os.Exit(1)
	}
	st, err := store.Open(filepath.Join(dataDir, "rospanel.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "database error:", err)
		os.Exit(1)
	}
	defer st.Close()
	set, err := st.GetSettings()
	if err != nil {
		fmt.Fprintln(os.Stderr, "settings error:", err)
		os.Exit(1)
	}
	host := strings.TrimSpace(set.Host)
	if host == "" {
		host = "<domain-not-configured>"
	}
	secret := strings.TrimSpace(set.PanelSecretPath)
	if secret == "" {
		fmt.Println("The panel's secret path is not set yet (first run?).")
		return
	}
	fmt.Printf("Panel: https://%s/%s/\n", host, secret)
	fmt.Printf("Subscriptions: https://%s/%s/<token>\n", host, set.SubPathOr())
	if strings.TrimSpace(set.TGBotToken) == "" && set.TGBotEnabled {
		fmt.Println("WARNING: the admin bot is enabled but its token is empty (check secrets.key).")
	}
}

// runTOTP is the way back in after a lost phone: no recovery codes to store next to
// the password, just this. It needs the machine — and whoever has that already holds
// the database and the key that decrypts it, so it hands out nothing new.
//
// With no arguments it lists who has a second factor, because the first thing an
// operator does here is check whether they are looking at the right account.
func runTOTP(dataDir string, args []string) {
	// A missing or unusable key must not block this command: neither listing nor
	// resetting reads a secret back (the reset writes empty columns), and this is the
	// way back in when something IS wrong with the key — a panel that refuses the login
	// because it can't decrypt a second factor is exactly when an operator runs it.
	if err := datasec.Init(dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: secrets key unavailable (%v) — continuing, this command does not need it\n", err)
	}
	st, err := store.Open(filepath.Join(dataDir, "rospanel.db"))
	if err != nil {
		log.Fatalf("totp: open store: %v", err)
	}
	defer st.Close()

	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list", "status":
		admins, err := st.ListAdmins()
		if err != nil {
			log.Fatalf("totp: %v", err)
		}
		for _, a := range admins {
			state := "off"
			if a.TOTPEnabled {
				state = "ON"
			}
			fmt.Printf("  %-20s %s\n", a.Username, state)
		}
		fmt.Println("\nRemove one with: rospanel totp reset <login>")
	case "reset", "off", "disable":
		login := firstPositional(args[1:])
		if login == "" {
			fmt.Fprintln(os.Stderr, "usage: rospanel totp reset <login>")
			os.Exit(1)
		}
		ok, err := st.DisableAdminTOTPByName(login)
		if err != nil {
			log.Fatalf("totp: %v", err)
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "no admin named %q\n", login)
			os.Exit(1)
		}
		fmt.Printf("Two-factor authentication removed for %s — they sign in with the password alone now.\n", login)
	default:
		fmt.Fprintf(os.Stderr, "unknown totp subcommand %q (try: list, reset <login>)\n", sub)
		os.Exit(1)
	}
}

// runHost prints or sets the panel host (domain or IP). Setting it drops the
// current certificate so a fresh one is issued for the new host on restart, then
// restarts the service (when managed by systemd) to apply it.
func runHost(dataDir string, args []string) {
	if err := datasec.Init(dataDir); err != nil {
		log.Fatalf("host: secrets key: %v", err)
	}
	st, err := store.Open(filepath.Join(dataDir, "rospanel.db"))
	if err != nil {
		log.Fatalf("host: open store: %v", err)
	}
	defer st.Close()
	cur, err := st.GetSettings()
	if err != nil {
		log.Fatalf("host: %v", err)
	}
	host := core.NormalizeACMEHost(strings.TrimSpace(firstPositional(args)))
	if host == "" {
		// No target given → just report the current host.
		if cur.Host == "" {
			log.Print("no host configured yet")
		} else {
			log.Printf("current host: %s", cur.Host)
		}
		return
	}
	if !hasYesFlag(args) && !confirmTTY(fmt.Sprintf(
		"Change the panel address to %q?\n"+
			"  The TLS certificate will be reissued (port 80 must be open), and clients\n"+
			"  and subscriptions will need the new address. The service will restart.\n"+
			"Continue? [y/N]: ", host)) {
		fmt.Println("Cancelled.")
		return
	}
	certPath := filepath.Join(dataDir, "certs", "cert.pem")
	keyPath := filepath.Join(dataDir, "certs", "key.pem")
	if err := st.SetTLS(host, host, model.TLSModeACME, certPath, keyPath); err != nil {
		log.Fatalf("host: %v", err)
	}
	// Drop the existing cert so the next boot issues a fresh one for the new host
	// (otherwise the still-valid old cert would be kept).
	_ = os.Remove(certPath)
	_ = os.Remove(keyPath)
	log.Printf("host set to %q", host)

	if _, err := exec.LookPath("systemctl"); err == nil {
		log.Print("restarting rospanel to issue the certificate…")
		c := exec.Command("systemctl", "restart", "rospanel")
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			log.Printf("auto-restart failed: %v — restart the panel manually", err)
		}
	} else {
		log.Print("restart the panel to issue the certificate")
	}
}

const (
	systemdUnitPath = "/etc/systemd/system/rospanel.service"
	installBinPath  = "/usr/local/bin/rospanel"
)

// runInstall installs rospanel as a systemd service: copy the running binary to
// /usr/local/bin, write a unit (carrying through any ROSPANEL_HOST /
// ROSPANEL_ACME_EMAIL / XRAY_BIN set in the current environment), then enable and
// (re)start it. Idempotent — re-run to update the unit or the binary.
func runInstall() {
	if runtime.GOOS != "linux" {
		log.Fatal("install: systemd setup is Linux-only")
	}
	if os.Geteuid() != 0 {
		log.Fatal("install: run as root (sudo)")
	}
	self, err := os.Executable()
	if err != nil {
		log.Fatalf("install: locate binary: %v", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
		self = resolved
	}
	if self != installBinPath {
		if err := copyFile(self, installBinPath, 0o755); err != nil {
			log.Fatalf("install: copy binary to %s: %v", installBinPath, err)
		}
		log.Printf("install: copied binary → %s", installBinPath)
	}

	dataDir := env("ROSPANEL_DATA", "/var/lib/rospanel")
	envLines := []string{
		"Environment=ROSPANEL_DATA=" + dataDir,
		// Internal loopback for the VLESS default fallback. Kept off 8080 so that
		// port is free for the optional VLESS-REALITY inbound.
		"Environment=ROSPANEL_ADMIN_ADDR=127.0.0.1:8080",
	}
	// Carry through optional config the operator passed when running install.
	for _, k := range []string{"ROSPANEL_HOST", "ROSPANEL_ACME_EMAIL", "XRAY_BIN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			envLines = append(envLines, "Environment="+k+"="+v)
		}
	}

	unit := "[Unit]\n" +
		"Description=RosPanel VPN Panel\n" +
		"After=network-online.target\n" +
		"Wants=network-online.target\n\n" +
		"[Service]\n" +
		"Type=simple\n" +
		strings.Join(envLines, "\n") + "\n" +
		"ExecStart=" + installBinPath + "\n" +
		"Restart=always\n" +
		"RestartSec=3\n" +
		// Signal only the panel, not everything in the cgroup. The default
		// (control-group) SIGTERMs the Xray child too, so it would die on its own
		// before the panel could mark the stop intentional — reported as a crash on
		// every restart. The panel stops Xray itself; KillMode=mixed still SIGKILLs
		// the whole group if the timeout expires, so nothing can be left behind.
		"KillMode=mixed\n" +
		// The panel still runs as root (it execs Xray, runs iptables/nft for the
		// brute-guard + Hysteria port-hopping, writes net.* sysctls for BBR, and
		// self-updates its own binary in /usr/local/bin). Rather than drop the user,
		// we shrink what that root can do: capabilities are pinned to exactly the two
		// the service needs, NoNewPrivileges blocks any setuid escalation, and the
		// filesystem/namespace is sandboxed so an RCE can't roam the host. The two
		// ReadWritePaths cover self-update (binary) and re-install (unit); the data
		// dir is writable via StateDirectory. Note: ProtectKernelTunables and
		// ProtectKernelModules are deliberately NOT set — they would break the BBR
		// sysctl write and nft/iptables module autoload respectively.
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_ADMIN\n" +
		"AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN\n" +
		"NoNewPrivileges=yes\n" +
		"ProtectSystem=strict\n" +
		"ReadWritePaths=/usr/local/bin /etc/systemd/system\n" +
		"ProtectHome=yes\n" +
		"PrivateTmp=yes\n" +
		"ProtectControlGroups=yes\n" +
		"ProtectClock=yes\n" +
		"RestrictSUIDSGID=yes\n" +
		"RestrictRealtime=yes\n" +
		"LockPersonality=yes\n" +
		"RemoveIPC=yes\n" +
		"StateDirectory=rospanel\n\n" +
		"[Install]\n" +
		"WantedBy=multi-user.target\n"
	if err := os.WriteFile(systemdUnitPath, []byte(unit), 0o644); err != nil {
		log.Fatalf("install: write unit: %v", err)
	}
	log.Printf("install: wrote %s", systemdUnitPath)

	for _, args := range [][]string{{"daemon-reload"}, {"enable", "rospanel"}, {"restart", "rospanel"}} {
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("install: systemctl %s: %v", strings.Join(args, " "), err)
		}
	}
	log.Print("install: done — service enabled and started")
	log.Print("first-run credentials: journalctl -u rospanel | grep -A6 FIRST-RUN")
}

// runUninstall stops/disables the service and removes the unit file. Data under
// ROSPANEL_DATA is left untouched.
func runUninstall(args []string) {
	if os.Geteuid() != 0 {
		log.Fatal("uninstall: run as root (sudo)")
	}
	if !hasYesFlag(args) && !confirmTTY(
		"Remove the rospanel systemd service? The panel will be stopped.\n"+
			"Data in the data dir (/var/lib/rospanel) is kept, and the binary is not removed.\n"+
			"Continue? [y/N]: ") {
		fmt.Println("Cancelled.")
		return
	}
	_ = exec.Command("systemctl", "disable", "--now", "rospanel").Run()
	if err := os.Remove(systemdUnitPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("uninstall: remove unit: %v", err)
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	log.Printf("uninstall: removed %s (data left in place)", systemdUnitPath)
}

// runService controls the systemd unit: start / stop / restart / status.
func runService(action string) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		log.Fatalf("%s: systemctl not found (service control needs Linux + systemd)", action)
	}
	c := exec.Command("systemctl", action, "rospanel")
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	err := c.Run()
	if action == "status" {
		return // `status` exits non-zero when inactive; its output is already shown
	}
	if err != nil {
		log.Fatalf("%s: failed (%v) — needs root, try sudo", action, err)
	}
	log.Printf("%s: done", action)
}

// copyFile copies src to dst atomically (write to .new, then rename) with mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// runUpdate is the `rospanel update [-y]` CLI: check the baked-in GitHub repo for
// a newer release, ask for confirmation, then download + verify + atomically swap
// the binary (snapshotting the DB first) and restart the service. `-y` skips the
// prompt for non-interactive use.
func runUpdate(args []string) {
	yes := hasYesFlag(args)
	ctx := context.Background()
	repo := updater.Repo
	if r := strings.TrimSpace(os.Getenv("ROSPANEL_REPO")); r != "" {
		repo = r
	}

	fmt.Printf("Current version: v%s\n", version.Version)
	rel, err := updater.Latest(ctx, repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	if !updater.IsNewer(rel.Version, version.Version) {
		fmt.Println("You are on the latest version.")
		return
	}
	if rel.AssetURL == "" {
		fmt.Fprintf(os.Stderr, "Release v%s has no %s asset.\n", rel.Version, updater.AssetName)
		os.Exit(1)
	}

	fmt.Printf("Version v%s is available.\n", rel.Version)
	if !yes && !confirmTTY(fmt.Sprintf(
		"Update v%s → v%s? The service restarts and connections drop briefly. [y/N]: ",
		version.Version, rel.Version)) {
		fmt.Println("Cancelled.")
		return
	}

	fmt.Println("Downloading and installing…")
	dataDir := resolveDataDir()
	backupFn := func() error {
		return backup.Create(dataDir, filepath.Join(dataDir, "pre-update-backup.tgz"))
	}
	if err := updater.Apply(ctx, rel, backupFn); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	fmt.Println("Restarting the service…")
	if err := exec.Command("systemctl", "restart", "rospanel").Run(); err != nil {
		fmt.Fprintf(os.Stderr,
			"The binary was updated but the restart failed: %v\nRun it manually: systemctl restart rospanel\n", err)
		os.Exit(1)
	}
	fmt.Printf("Done — updated to v%s.\n", rel.Version)
}

// runReset is the `rospanel reset [-y]` CLI: a FULL factory reset. It stops the
// service, deletes the database (ALL settings, users AND admins), then starts it
// again — which re-bootstraps a clean install (fresh admin credentials printed to
// the log, host/domain/secret regenerated, first-run wizard). TLS cert files on
// disk are left in place (reused once a domain is configured again).
func runReset(args []string) {
	if !hasYesFlag(args) && !confirmTTY(
		"FULL FACTORY RESET of the panel.\n"+
			"EVERYTHING will be WIPED: users, the admin account, domain/TLS, the secret\n"+
			"path, protocols, ports, routing, proxies, DNS — the entire database.\n"+
			"After the reset: login admin/admin, path /rospanel/, first-run setup again.\n"+
			"This cannot be undone. Continue? [y/N]: ") {
		fmt.Println("Cancelled.")
		return
	}

	dataDir := resolveDataDir()
	// Stop the service so the DB files aren't held open (and re-created) while we
	// delete them. Refuse to delete the DB out from under a still-live process.
	hasSystemctl := false
	if _, err := exec.LookPath("systemctl"); err == nil {
		hasSystemctl = true
		_ = exec.Command("systemctl", "stop", "rospanel").Run()
		if out, _ := exec.Command("systemctl", "is-active", "rospanel").Output(); strings.TrimSpace(string(out)) == "active" {
			log.Fatal("reset: the rospanel service is still active — stop it manually (systemctl stop rospanel) and retry")
		}
	} else {
		log.Print("reset: systemctl not found — stop the running panel manually before resetting, or deleting the database will not take effect")
	}

	removed := false
	for _, f := range []string{"rospanel.db", "rospanel.db-wal", "rospanel.db-shm"} {
		err := os.Remove(filepath.Join(dataDir, f))
		switch {
		case err == nil:
			removed = true
		case os.IsNotExist(err):
			// nothing to remove
		default:
			fmt.Fprintf(os.Stderr, "Could not remove %s: %v\n", f, err)
			os.Exit(1)
		}
	}
	if !removed {
		fmt.Fprintln(os.Stderr, "No database found — nothing to reset.")
	}

	if hasSystemctl {
		if err := exec.Command("systemctl", "start", "rospanel").Run(); err != nil {
			fmt.Fprintf(os.Stderr,
				"The database was deleted but the service did not start: %v\nRun it manually: systemctl start rospanel\n", err)
			os.Exit(1)
		}
	}
	fmt.Println("Done — the panel has been reset to factory settings.")
	fmt.Println("Login: admin / admin · panel path: /rospanel/ (the setup wizard will ask you to change the password).")
}

// hasYesFlag reports whether the CLI args carry a non-interactive confirm flag.
func hasYesFlag(args []string) bool {
	for _, a := range args {
		if a == "-y" || a == "--yes" || a == "--force" {
			return true
		}
	}
	return false
}

// confirmTTY prompts on stdin and returns true only for an explicit yes.
func confirmTTY(prompt string) bool {
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	// "д"/"да" stay accepted: an operator on a Russian keyboard layout answering a
	// destructive prompt should not have their intent silently read as "no".
	case "y", "yes", "д", "да":
		return true
	}
	return false
}

// runRescue is the way back in when an operator is locked out of the panel: a lost
// password, a lost second factor, or an admin roster with no reachable owner. It runs
// on the box with the data directory, which is the trust boundary — anyone who can
// run it already has the database. It deliberately overlaps `totp reset` (which stays)
// and adds the password and owner halves the web UI can't offer a locked-out admin.
func runRescue(dataDir string, args []string) {
	// The secrets key is not needed here — nothing reads a secret back, and a broken
	// key is one of the reasons an admin is locked out — so a missing one only warns.
	if err := datasec.Init(dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: secrets key unavailable (%v) — continuing, rescue does not need it\n", err)
	}
	st, err := store.Open(filepath.Join(dataDir, "rospanel.db"))
	if err != nil {
		log.Fatalf("rescue: open store: %v", err)
	}
	defer st.Close()

	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list", "status":
		admins, err := st.ListAdmins()
		if err != nil {
			log.Fatalf("rescue: %v", err)
		}
		fmt.Printf("  %-20s %-9s %-4s %s\n", "LOGIN", "ROLE", "2FA", "LAST LOGIN")
		for _, a := range admins {
			twoFA := "off"
			if a.TOTPEnabled {
				twoFA = "ON"
			}
			last := "never"
			if a.LastLoginAt > 0 {
				last = time.Unix(a.LastLoginAt, 0).Format("2006-01-02 15:04")
			}
			fmt.Printf("  %-20s %-9s %-4s %s\n", a.Username, a.Role, twoFA, last)
		}
		fmt.Println("\n  rescue password <login>   set a new password (printed once)")
		fmt.Println("  rescue unlock <login>     new password AND remove the second factor")
		fmt.Println("  rescue owner <login>      create an owner, or promote+unlock an existing admin")

	case "password":
		login := firstPositional(args[1:])
		if login == "" {
			fmt.Fprintln(os.Stderr, "usage: rospanel rescue password <login>")
			os.Exit(1)
		}
		rescueSetPassword(st, login, false)

	case "unlock":
		login := firstPositional(args[1:])
		if login == "" {
			fmt.Fprintln(os.Stderr, "usage: rospanel rescue unlock <login>")
			os.Exit(1)
		}
		rescueSetPassword(st, login, true)

	case "owner":
		login := firstPositional(args[1:])
		if login == "" {
			fmt.Fprintln(os.Stderr, "usage: rospanel rescue owner <login>")
			os.Exit(1)
		}
		rescueOwner(st, login)

	default:
		fmt.Fprintf(os.Stderr, "unknown rescue command %q\n", sub)
		fmt.Fprintln(os.Stderr, "usage: rospanel rescue [list|password <login>|unlock <login>|owner <login>]")
		os.Exit(2)
	}
}

// rescueSetPassword resets one admin's password, printing the new one exactly once,
// and (when unlock) removes the second factor too. must_change is set so the operator
// picks their own password on the next sign-in and this one stops working.
func rescueSetPassword(st *store.Store, login string, unlock bool) {
	id, _, _, err := st.GetAdminAuth(login)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no admin named %q\n", login)
		os.Exit(1)
	}
	pw := rescueResetPassword(st, id)
	if unlock {
		if _, err := st.DisableAdminTOTPByName(login); err != nil {
			log.Fatalf("rescue: clear 2FA: %v", err)
		}
	}
	rescueAudit(st, model.AuditAdminPasswordReset, login, unlock)
	printRescueCredentials(login, pw, unlock)
}

// rescueOwner promotes an existing admin to owner and unlocks them, or creates a new
// owner when the login is unknown — the escape hatch for a roster with no way in.
func rescueOwner(st *store.Store, login string) {
	pw, err := auth.RandomPassword()
	if err != nil {
		log.Fatalf("rescue: generate password: %v", err)
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		log.Fatalf("rescue: hash password: %v", err)
	}
	// "Exactly one owner" is an invariant the panel relies on, so a rescue that grants
	// ownership must also TAKE it from whoever holds it — otherwise two owners exist,
	// which is a worse mess than the lockout being fixed. Demote the current owner (if
	// it isn't the target) before promoting.
	admins, err := st.ListAdmins()
	if err != nil {
		log.Fatalf("rescue: list admins: %v", err)
	}
	for _, a := range admins {
		if a.Role == model.RoleOwner && !strings.EqualFold(a.Username, login) {
			if err := st.SetAdminRole(a.ID, model.RoleAdmin); err != nil {
				log.Fatalf("rescue: demote current owner: %v", err)
			}
			fmt.Printf("demoted the previous owner %q to administrator\n", a.Username)
			rescueAudit(st, model.AuditAdminRoleChanged, a.Username, false)
		}
	}

	if id, _, _, err := st.GetAdminAuth(login); err == nil {
		// Exists: make them owner, reset the password, clear any second factor.
		if err := st.SetAdminRole(id, model.RoleOwner); err != nil {
			log.Fatalf("rescue: set role: %v", err)
		}
		if err := st.UpdateAdminPassword(id, hash, true); err != nil {
			log.Fatalf("rescue: set password: %v", err)
		}
		if _, err := st.DisableAdminTOTPByName(login); err != nil {
			log.Fatalf("rescue: clear 2FA: %v", err)
		}
		rescueAudit(st, model.AuditAdminPasswordReset, login, true)
		fmt.Printf("promoted %q to owner and unlocked it\n", login)
		printRescueCredentials(login, pw, true)
		return
	}
	if _, err := st.CreateAdmin(login, hash, model.RoleOwner, true); err != nil {
		log.Fatalf("rescue: create owner: %v", err)
	}
	rescueAudit(st, model.AuditAdminCreated, login, false)
	fmt.Printf("created owner %q\n", login)
	printRescueCredentials(login, pw, false)
}

// rescueResetPassword writes a fresh random password for one admin and returns it.
func rescueResetPassword(st *store.Store, id int64) string {
	pw, err := auth.RandomPassword()
	if err != nil {
		log.Fatalf("rescue: generate password: %v", err)
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		log.Fatalf("rescue: hash password: %v", err)
	}
	if err := st.UpdateAdminPassword(id, hash, true); err != nil {
		log.Fatalf("rescue: set password: %v", err)
	}
	return pw
}

// rescueAudit records the rescue on the admin trail, so a password reset done from a
// shell still shows up next to the ones done in the UI. Best-effort: a rescue must
// not fail because the trail could not be written.
func rescueAudit(st *store.Store, action, login string, twoFACleared bool) {
	details := map[string]any{"via": "rescue-cli"}
	if twoFACleared {
		details["cleared_2fa"] = true
	}
	_ = st.AddAdminAudit(model.AdminAudit{
		Action: action, Target: login, ActorKind: "system", ActorName: "rescue-cli",
		Details: details,
	})
}

// printRescueCredentials shows the new credentials once, with the must-change note.
func printRescueCredentials(login, password string, unlocked bool) {
	bar := strings.Repeat("=", 56)
	extra := ""
	if unlocked {
		extra = "\n Two-factor : removed"
	}
	fmt.Printf("\n%s\n RESCUE CREDENTIALS (shown once — sign in and change them)\n%s\n"+
		" Login      : %s\n Password   : %s%s\n%s\n",
		bar, bar, login, password, extra, bar)
}

// checkRestoreSchema unpacks an archive to a throwaway directory and refuses it when its
// database was written by a panel newer than this binary. Fails closed: a schema it
// cannot read is not one to take a chance on.
func checkRestoreSchema(src string) error {
	dir, err := os.MkdirTemp("", "rospanel-restore-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := backup.Restore(src, dir); err != nil {
		return fmt.Errorf("archive is unreadable: %w", err)
	}
	v, err := store.DBSchemaVersion(filepath.Join(dir, "rospanel.db"))
	if err != nil {
		return fmt.Errorf("could not read the archive's schema version: %w", err)
	}
	if v > store.SchemaVersion() {
		return fmt.Errorf("the backup was written by a newer panel (schema %d, this build knows %d) — "+
			"update the panel first", v, store.SchemaVersion())
	}
	return nil
}
