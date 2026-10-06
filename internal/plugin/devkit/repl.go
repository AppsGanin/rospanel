package devkit

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const replHelp = `commands:
  event <name> [json]        deliver an event to onEvent, e.g. event user.created {"id":1,"name":"Ann"}
  call <export> [json]       call an export, e.g. call payment.create {"order_id":1,"amount_rub":100}
  cron <name>                run a cron job
  kv [prefix]                list the plugin's kv
  sql <query>                query the plugin's database
  mock api <METHOD> <path> <json>   answer panel.api with json
  mock http <url-prefix> <json>     answer panel.http.fetch with json
  logs                       the plugin's log so far
  reload                     reload now (files are watched anyway)
  help, quit`

// Dev runs a REPL against the plugin in dir, reloading it when its files change.
// With remote set, panel.api goes to that panel; fetches go out for real unless
// mocked.
func Dev(ctx context.Context, dir, panelVersion string, remote *Remote, in io.Reader, out io.Writer) error {
	raw, _, err := Pack(dir)
	if err != nil {
		return err
	}
	cfg, err := ReadDevConfig(dir)
	if err != nil {
		return err
	}
	h, err := Start(ctx, raw, Options{Settings: cfg.Settings, PanelVersion: panelVersion})
	if err != nil {
		return err
	}
	defer h.Close()
	h.Remote, h.RealHTTP = remote, true
	fmt.Fprintf(out, "%s is running. %s\n", h.ID, "Type help for commands.")

	reload := func() {
		raw, _, err := Pack(dir)
		if err == nil {
			err = h.Reload(ctx, raw)
		}
		if err != nil {
			fmt.Fprintf(out, "reload failed: %v\n", err)
			return
		}
		fmt.Fprintln(out, "reloaded")
	}
	stamp := dirStamp(dir)
	watchCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-t.C:
				if s := dirStamp(dir); s != stamp {
					stamp = s
					reload()
				}
			}
		}
	}()

	printed := 0
	showLogs := func() {
		lines := h.Logs()
		for _, l := range lines[min(printed, len(lines)):] {
			fmt.Fprintf(out, "  [%s] %s\n", l.Level, l.Msg)
		}
		printed = len(lines)
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	fmt.Fprint(out, "> ")
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		cmd, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		var res json.RawMessage
		var err error
		switch cmd {
		case "":
		case "quit", "exit":
			return nil
		case "help":
			fmt.Fprintln(out, replHelp)
		case "event":
			name, data, _ := strings.Cut(rest, " ")
			res, err = h.Event(ctx, name, json.RawMessage(strings.TrimSpace(data)))
		case "call":
			name, data, _ := strings.Cut(rest, " ")
			var arg any
			if d := strings.TrimSpace(data); d != "" {
				arg = json.RawMessage(d)
			}
			res, err = h.Call(ctx, name, arg)
		case "cron":
			res, err = h.Call(ctx, rest, nil)
		case "kv":
			arg, _ := json.Marshal(map[string]any{"prefix": rest, "limit": 100})
			res, err = h.Host.DevOp(ctx, h.ID, "kv.list", arg)
		case "sql":
			arg, _ := json.Marshal(map[string]any{"sql": rest})
			res, err = h.Host.DevOp(ctx, h.ID, "db.query", arg)
		case "mock":
			err = h.replMock(rest)
		case "logs":
			printed = 0
		case "reload":
			reload()
		default:
			fmt.Fprintf(out, "unknown command %q — type help\n", cmd)
		}
		showLogs()
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", jsDetail(err))
		} else if res != nil {
			fmt.Fprintf(out, "%s\n", res)
		}
		fmt.Fprint(out, "> ")
	}
	return sc.Err()
}

func (h *Harness) replMock(rest string) error {
	kind, rest, _ := strings.Cut(rest, " ")
	switch kind {
	case "api":
		parts := strings.SplitN(rest, " ", 3)
		if len(parts) != 3 {
			return fmt.Errorf("mock api <METHOD> <path> <json>")
		}
		h.mockAPI(parts[0], parts[1], 200, json.RawMessage(parts[2]))
	case "http":
		prefix, body, ok := strings.Cut(rest, " ")
		if !ok {
			return fmt.Errorf("mock http <url-prefix> <json>")
		}
		h.mockHTTP(prefix, fetchResponse(200, body))
	default:
		return fmt.Errorf("mock api … or mock http …")
	}
	return nil
}

// dirStamp changes whenever a file of the plugin directory does.
func dirStamp(dir string) string {
	var b strings.Builder
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && p != dir && (strings.HasPrefix(info.Name(), ".") || info.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			fmt.Fprintf(&b, "%s:%d:%d;", p, info.Size(), info.ModTime().UnixNano())
		}
		return nil
	})
	return b.String()
}
