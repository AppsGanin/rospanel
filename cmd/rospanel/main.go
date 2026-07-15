// Command rospanel is the RosPanel VPN panel: boot the store, obtain a Let's Encrypt
// cert (domain or IP), generate the Xray config, and serve the admin API + the
// masquerade/subscription surface.
//
// main() handles only log setup and CLI dispatch; the server bootstrap/run path
// lives in service.go (runServer) and the subcommand implementations in cli.go.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // embed the IANA tz database so LoadLocation works on any host

	"github.com/AppsGanin/rospanel/internal/logbuf"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/version"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("rospanel: ")

	dataDir := resolveDataDir()

	// Stamp log lines in the operator's timezone rather than the server's system one
	// (a box in Europe/Berlin serving an operator on Europe/Moscow would otherwise
	// log an hour off from every other date the panel shows). Done here, before the
	// first line is logged, so the whole boot log is in one zone; core keeps it in
	// sync afterwards when the operator changes the setting.
	if tz := store.PeekTimezone(filepath.Join(dataDir, "rospanel.db")); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			logbuf.SetLocation(loc)
		}
	}

	// Tee all log output to: stderr (journald), the in-memory ring (dashboard log
	// viewer), and a size-rotated file under the data dir (10 MB × 5 backups). If
	// the file can't be opened we log a warning and carry on with the other sinks.
	sinks := []io.Writer{os.Stderr, logbuf.Default}
	if rf, err := logbuf.NewRotatingFile(filepath.Join(dataDir, "logs", "rospanel.log"), 10<<20, 5); err == nil {
		sinks = append(sinks, rf)
	} else {
		log.Printf("[WARN] file logging disabled: %v", err)
	}
	out := io.MultiWriter(sinks...)
	log.SetOutput(out)

	// Route slog (and, via slog.SetDefault, the standard log package) straight to
	// the sinks with an [INFO]/[WARN]/[ERROR] tag the dashboard log viewer filters
	// on. The handler must write to `out` DIRECTLY, not via log.Print: SetDefault
	// repoints the standard logger's output at this handler, so calling log.Print
	// here would loop back into the handler and self-deadlock on the log mutex.
	slog.SetDefault(slog.New(newSlogHandler(out)))

	// CLI subcommands. Without one, run the panel server (the systemd default).
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "backup":
			runBackup(dataDir, os.Args[2:])
		case "restore":
			runRestore(dataDir, os.Args[2:])
		case "host":
			runHost(dataDir, os.Args[2:])
		case "path":
			runPath(dataDir)
		case "install":
			runInstall()
		case "uninstall":
			runUninstall(os.Args[2:])
		case "start", "stop", "restart", "status":
			runService(os.Args[1])
		case "update":
			runUpdate(os.Args[2:])
		case "node":
			runNode(os.Args[2:])
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

// slogHandler writes slog records straight to the configured sinks (stderr,
// logbuf, rotating file) with a [LEVEL] tag the dashboard log viewer filters on.
// It writes to its own io.Writer — NOT log.Print — because slog.SetDefault
// repoints the standard log package at this handler; routing back through
// log.Print would recurse into Handle and self-deadlock on the log mutex.
type slogHandler struct {
	w  io.Writer
	mu *sync.Mutex // serialises writes so concurrent records don't interleave
}

func newSlogHandler(w io.Writer) *slogHandler {
	return &slogHandler{w: w, mu: &sync.Mutex{}}
}

func (*slogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *slogHandler) Handle(_ context.Context, r slog.Record) error {
	tag := "[INFO]"
	switch {
	case r.Level >= slog.LevelError:
		tag = "[ERROR]"
	case r.Level >= slog.LevelWarn:
		tag = "[WARN]"
	}
	// Timestamp first, matching Xray's own log format (2006/01/02 15:04:05.000000)
	// so panel and Xray lines read as one stream in the dashboard viewer. The level
	// tag stays intact — the viewer classifies lines by searching for [INFO]/[WARN]/
	// [ERROR] anywhere in them, not at the start.
	//
	// Rendered in the OPERATOR's configured timezone, not the server's system zone:
	// a box in Europe/Berlin serving an operator on Europe/Moscow would otherwise
	// stamp logs an hour off from every other date the panel shows.
	ts := r.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	var b strings.Builder
	b.WriteString(ts.In(logbuf.Location()).Format("2006/01/02 15:04:05.000000"))
	b.WriteByte(' ')
	b.WriteString(tag)
	b.WriteByte(' ')
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteByte('=')
		if a.Value.Kind() == slog.KindString {
			fmt.Fprintf(&b, "%q", a.Value.String())
		} else {
			fmt.Fprint(&b, a.Value.Any())
		}
		return true
	})
	b.WriteByte('\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *slogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *slogHandler) WithGroup(string) slog.Handler      { return h }

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envInt returns the integer value of an env var, or def when unset/unparseable.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
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
