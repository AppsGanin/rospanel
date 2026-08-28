package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/firewall"
	"github.com/Shu1t3/rospanel-shu1t3/internal/nodeagent"
	"github.com/Shu1t3/rospanel-shu1t3/internal/updater"
)

const (
	nodeUnitPath    = "/etc/systemd/system/rospanel-node.service"
	nodeStateDir    = "rospanel-node" // systemd StateDirectory → /var/lib/rospanel-node
	nodeDefaultData = "/var/lib/rospanel-node"
)

// runNode dispatches the `rospanel node <sub>` commands: install (join + systemd),
// run (the agent loop), set-panel, status, uninstall.
func runNode(args []string) {
	if len(args) == 0 {
		printNodeUsage(os.Stderr)
		os.Exit(2)
	}
	dataDir := nodeDataDir()
	switch args[0] {
	case "install":
		runNodeInstall(dataDir, args[1:])
	case "run":
		runNodeAgent(dataDir)
	case "set-panel":
		runNodeSetPanel(dataDir, args[1:])
	case "status":
		runNodeStatus(dataDir)
	case "uninstall":
		runNodeUninstall(args[1:])
	case "help", "--help", "-h":
		printNodeUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown node command %q\n\n", args[0])
		printNodeUsage(os.Stderr)
		os.Exit(2)
	}
}

// nodeDataDir resolves the node's data directory (separate from a panel's so the
// two can coexist on one box in dev): ROSPANEL_DATA if set, otherwise the standard
// node location when it exists, otherwise ./data-node.
func nodeDataDir() string {
	if v := os.Getenv("ROSPANEL_DATA"); v != "" {
		return v
	}
	if _, err := os.Stat(filepath.Join(nodeDefaultData, "node.json")); err == nil {
		return nodeDefaultData
	}
	return "./data-node"
}

// runNodeAgent runs the agent until SIGINT/SIGTERM (the systemd ExecStart entry).
//
// It joins first when ROSPANEL_JOIN is set and this data directory has no identity
// yet. That is for containers: `node install` writes a systemd unit, which a
// container has nowhere to put, so without this a Docker node needs a separate
// one-off join before the real command — two steps to describe in every compose
// file. With the join in the environment the node is one service that can be
// destroyed and recreated at will: the variable is only consulted when there is no
// node.json, so a restart re-reads the identity it already has and a spent join
// token in a stale compose file changes nothing.
func runNodeAgent(dataDir string) {
	if joinURL := strings.TrimSpace(os.Getenv("ROSPANEL_JOIN")); joinURL != "" {
		if _, err := nodeagent.LoadIdentity(dataDir); err != nil {
			insecure := isTrue(os.Getenv("ROSPANEL_JOIN_INSECURE"))
			log.Print("node: no identity yet — joining with ROSPANEL_JOIN")
			ident, jerr := nodeagent.Join(dataDir, joinURL, insecure)
			if jerr != nil {
				log.Fatalf("node: join: %v", jerr)
			}
			log.Printf("node: joined as node #%d (panel %s)", ident.NodeID, ident.PanelURL)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := nodeagent.Run(ctx, dataDir); err != nil {
		log.Fatalf("node: %v", err)
	}
	log.Print("node: stopped")
}

// isTrue reads the usual spellings of "yes" in an environment variable. Anything
// else — including an empty value — is false, so a variable left in a compose file
// as ROSPANEL_JOIN_INSECURE= does not quietly turn TLS verification off.
func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// runNodeInstall joins the node to the panel and installs the systemd unit.
func runNodeInstall(dataDir string, args []string) {
	var joinURL string
	insecure := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--join":
			if i+1 < len(args) {
				joinURL = args[i+1]
				i++
			}
		case "--insecure":
			insecure = true
		default:
			if strings.HasPrefix(args[i], "--join=") {
				joinURL = strings.TrimPrefix(args[i], "--join=")
			}
		}
	}
	if joinURL == "" {
		log.Fatal("node install: --join <url> is required (from the panel's Add-node dialog)")
	}
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		log.Fatal("node install: run as root (sudo)")
	}

	// Prefer the fixed system data dir for a real install so the systemd unit and
	// the join write to the same place.
	if os.Getenv("ROSPANEL_DATA") == "" && runtime.GOOS == "linux" && os.Geteuid() == 0 {
		dataDir = nodeDefaultData
	}

	log.Printf("node install: joining panel…")
	ident, err := nodeagent.Join(dataDir, joinURL, insecure)
	if err != nil {
		log.Fatalf("node install: %v", err)
	}
	log.Printf("node install: joined as node #%d (panel %s)", ident.NodeID, ident.PanelURL)

	if runtime.GOOS != "linux" {
		log.Print("node install: joined. systemd setup is Linux-only — run `rospanel node run` to start the agent.")
		return
	}
	installNodeSystemd(dataDir)
	log.Print("node install: done — the node is starting and will appear online in the panel shortly")
	log.Print("logs: journalctl -u rospanel-node -f")
}

// installNodeSystemd copies the binary and writes+starts the rospanel-node unit.
func installNodeSystemd(dataDir string) {
	self, err := os.Executable()
	if err != nil {
		log.Fatalf("node install: locate binary: %v", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
		self = resolved
	}

	// A box can't run both the panel and a node — they'd fight over :443. If a panel
	// service is present here (e.g. a mistaken `rospanel install` on this server),
	// stop and disable it so the node's Xray can bind the port. Best-effort.
	if out, _ := exec.Command("systemctl", "is-enabled", "rospanel").Output(); len(out) > 0 {
		log.Print("node install: found a rospanel PANEL service on this box — disabling it (a node can't also be a panel)")
		_ = exec.Command("systemctl", "disable", "--now", "rospanel").Run()
	}

	if self != installBinPath {
		if err := copyFile(self, installBinPath, 0o755); err != nil {
			log.Fatalf("node install: copy binary to %s: %v", installBinPath, err)
		}
		log.Printf("node install: copied binary → %s", installBinPath)
	}

	repo := updater.Repo
	if r := strings.TrimSpace(os.Getenv("ROSPANEL_REPO")); r != "" {
		repo = r
	}
	envLines := []string{
		"Environment=ROSPANEL_DATA=" + dataDir,
		"Environment=ROSPANEL_REPO=" + repo,
	}
	if v := strings.TrimSpace(os.Getenv("XRAY_BIN")); v != "" {
		envLines = append(envLines, "Environment=XRAY_BIN="+v)
	}
	// Same hardening profile as the panel unit: root (for Xray + nft port-hopping +
	// BBR sysctl + self-update), but capability-pinned and filesystem-sandboxed.
	unit := "[Unit]\n" +
		"Description=RosPanel VPN Node\n" +
		"After=network-online.target\n" +
		"Wants=network-online.target\n\n" +
		"[Service]\n" +
		"Type=simple\n" +
		strings.Join(envLines, "\n") + "\n" +
		"ExecStart=" + installBinPath + " node run\n" +
		"Restart=always\n" +
		"RestartSec=3\n" +
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
		"StateDirectory=" + nodeStateDir + "\n\n" +
		"[Install]\n" +
		"WantedBy=multi-user.target\n"
	if err := os.WriteFile(nodeUnitPath, []byte(unit), 0o644); err != nil {
		log.Fatalf("node install: write unit: %v", err)
	}
	log.Printf("node install: wrote %s", nodeUnitPath)
	for _, a := range [][]string{{"daemon-reload"}, {"enable", "rospanel-node"}, {"restart", "rospanel-node"}} {
		cmd := exec.Command("systemctl", a...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("node install: systemctl %s: %v", strings.Join(a, " "), err)
		}
	}

	// Ensure system firewall (UFW) is configured and standard ports (80/tcp, 443/tcp) are open.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	nodeRules := []firewall.Rule{
		firewall.TCPRule(80, "http-redirect"),
		firewall.TCPRule(443, "vless"),
	}
	if err := firewall.Sync(ctx, nodeRules); err != nil {
		log.Printf("node install: firewall setup warning: %v", err)
	} else {
		log.Print("node install: firewall (ufw) configured and enabled")
	}
}

// runNodeSetPanel rewrites the panel URL in node.json (recovery when the panel's
// domain changes and the broadcast didn't reach this node).
func runNodeSetPanel(dataDir string, args []string) {
	if len(args) == 0 {
		log.Fatal("node set-panel: <url> required (e.g. https://newpanel.example.com)")
	}
	ident, err := nodeagent.LoadIdentity(dataDir)
	if err != nil {
		log.Fatalf("node set-panel: %v", err)
	}
	ident.PanelURL = strings.TrimRight(strings.TrimSpace(args[0]), "/")
	if err := ident.Save(dataDir); err != nil {
		log.Fatalf("node set-panel: %v", err)
	}
	log.Printf("node set-panel: panel URL updated to %s (restart the service to apply)", ident.PanelURL)
}

func runNodeStatus(dataDir string) {
	s, err := nodeagent.Status(dataDir)
	if err != nil {
		log.Fatalf("node status: %v", err)
	}
	fmt.Print(s)
}

// runNodeUninstall stops the node service and removes its unit. Data is kept.
func runNodeUninstall(args []string) {
	if os.Geteuid() != 0 {
		log.Fatal("node uninstall: run as root (sudo)")
	}
	if !hasYesFlag(args) && !confirmTTY(
		"Remove the rospanel-node systemd service? The node will be stopped.\n"+
			"Node data is kept and the binary is not removed. Continue? [y/N]: ") {
		fmt.Println("Cancelled.")
		return
	}
	_ = exec.Command("systemctl", "disable", "--now", "rospanel-node").Run()
	if err := os.Remove(nodeUnitPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("node uninstall: remove unit: %v", err)
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	log.Printf("node uninstall: removed %s (data left in place)", nodeUnitPath)
}

func printNodeUsage(w *os.File) {
	fmt.Fprint(w, `rospanel node — run this server as a panel-managed VPN node

Usage:
  rospanel node install --join '<url>' [--insecure]   join a panel and install the service
  rospanel node run                                    run the node agent (systemd entry)
  rospanel node set-panel <url>                        point the node at a new panel URL
  rospanel node status                                 show local node status
  rospanel node uninstall [-y]                         remove the node service

The --join URL comes from the panel's "Add node" dialog.

Environment:
  ROSPANEL_JOIN            join URL for "node run" to use when this data directory
                           has no node.json yet (containers: no systemd to install)
  ROSPANEL_JOIN_INSECURE   1/true/yes — skip TLS verification on that join, for a
                           panel still on a self-signed certificate
  ROSPANEL_DATA            where the node keeps its state (default `+nodeDefaultData+`)
`)
}
