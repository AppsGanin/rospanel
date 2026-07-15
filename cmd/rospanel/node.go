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

	"github.com/AppsGanin/rospanel/internal/nodeagent"
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
		fmt.Fprintf(os.Stderr, "неизвестная node-команда %q\n\n", args[0])
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
func runNodeAgent(dataDir string) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := nodeagent.Run(ctx, dataDir); err != nil {
		log.Fatalf("node: %v", err)
	}
	log.Print("node: stopped")
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

	envLines := []string{"Environment=ROSPANEL_DATA=" + dataDir}
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
		"Удалить systemd-сервис rospanel-node? Нода будет остановлена.\n"+
			"Данные ноды сохранятся, бинарь не удаляется. Продолжить? [y/N]: ") {
		fmt.Println("Отменено.")
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
`)
}
