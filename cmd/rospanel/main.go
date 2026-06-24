// Command rospanel is the RosPanel VPN panel: boot the store, obtain a Let's Encrypt
// cert (domain or IP), generate the Xray config, and serve the admin API + the
// masquerade/subscription surface.
//
// main() handles only log setup and CLI dispatch; the server bootstrap/run path
// lives in service.go (runServer) and the subcommand implementations in cli.go.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	_ "time/tzdata" // embed the IANA tz database so LoadLocation works on any host

	"github.com/msTimofeev/rospanel/internal/logbuf"
	"github.com/msTimofeev/rospanel/internal/version"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("rospanel: ")

	dataDir := resolveDataDir()

	// Tee all log output to: stderr (journald), the in-memory ring (dashboard log
	// viewer), and a size-rotated file under the data dir (10 MB × 5 backups). If
	// the file can't be opened we log a warning and carry on with the other sinks.
	sinks := []io.Writer{os.Stderr, logbuf.Default}
	if rf, err := logbuf.NewRotatingFile(filepath.Join(dataDir, "logs", "rospanel.log"), 10<<20, 5); err == nil {
		sinks = append(sinks, rf)
	} else {
		log.Printf("[WARN] file logging disabled: %v", err)
	}
	log.SetOutput(io.MultiWriter(sinks...))

	// CLI subcommands. Without one, run the panel server (the systemd default).
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "backup":
			runBackup(dataDir, os.Args[2:])
		case "restore":
			runRestore(dataDir, os.Args[2:])
		case "host":
			runHost(dataDir, os.Args[2:])
		case "install":
			runInstall()
		case "uninstall":
			runUninstall(os.Args[2:])
		case "start", "stop", "restart", "status":
			runService(os.Args[1])
		case "update":
			runUpdate(os.Args[2:])
		case "reset":
			runReset(os.Args[2:])
		case "version", "--version", "-v":
			fmt.Println("rospanel " + version.Version)
		case "help", "--help", "-h":
			printUsage(os.Stdout)
		default:
			fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n", os.Args[1])
			printUsage(os.Stderr)
			os.Exit(2)
		}
		return
	}

	runServer(dataDir)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// resolveDataDir picks the data directory: ROSPANEL_DATA if set, otherwise the
// standard service location when a panel is installed there, otherwise local
// ./data. The installed location is preferred over the cwd so a destructive CLI
// command (reset/backup/host) run from an arbitrary directory targets the real
// panel and can't be silently shadowed by a stray ./data in the current dir.
func resolveDataDir() string {
	if v := os.Getenv("ROSPANEL_DATA"); v != "" {
		return v
	}
	if _, err := os.Stat("/var/lib/rospanel/rospanel.db"); err == nil {
		return "/var/lib/rospanel"
	}
	return "./data"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
