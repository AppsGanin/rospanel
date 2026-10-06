package devkit

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/plugin/jsvm"
)

//go:embed testprelude.js
var testPrelude string

// TestResult is one test of test.js.
type TestResult struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Stack string `json:"stack"`
}

// RunTests runs dir/test.js against the plugin in dir and writes a report to out.
// test.js runs in a VM of its own; its plugin.* calls reach the plugin through the
// host, the way the panel would make them, and mock.* answers what the plugin asks
// of the panel and the internet.
func RunTests(ctx context.Context, dir, panelVersion string, out io.Writer) (results []TestResult, err error) {
	src, err := os.ReadFile(filepath.Join(dir, "test.js"))
	if err != nil {
		return nil, fmt.Errorf("test.js: %w", err)
	}
	raw, _, err := Pack(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := ReadDevConfig(dir)
	if err != nil {
		return nil, err
	}
	h, err := Start(ctx, raw, Options{Settings: cfg.Settings, PanelVersion: panelVersion})
	if err != nil {
		return nil, err
	}
	defer h.Close()

	engine := jsvm.NewEngine(jsvm.Options{})
	defer engine.Close(ctx)
	vm, err := engine.NewVM(ctx, jsvm.Limits{Memory: 64 << 20})
	if err != nil {
		return nil, err
	}
	defer vm.Close()
	host := h.testHost(out)
	if err := vm.Script(ctx, "testprelude.js", testPrelude, 10*time.Second, host); err != nil {
		return nil, fmt.Errorf("test prelude: %w", err)
	}
	if err := vm.Load(ctx, string(src), 30*time.Second, host); err != nil {
		return nil, fmt.Errorf("test.js: %w", jsDetail(err))
	}
	if err := vm.Load(ctx, `export function run() { return globalThis.__run(); }`, time.Second, host); err != nil {
		return nil, err
	}
	res, err := vm.Call(ctx, "run", nil, 10*time.Minute, host)
	if err != nil {
		return nil, fmt.Errorf("test.js: %w", jsDetail(err))
	}
	if err := json.Unmarshal(res, &results); err != nil {
		return nil, err
	}
	for _, r := range results {
		if r.OK {
			fmt.Fprintf(out, "  ✓ %s\n", r.Name)
			continue
		}
		fmt.Fprintf(out, "  ✗ %s\n      %s\n", r.Name, strings.ReplaceAll(r.Error, "\n", "\n      "))
	}
	return results, nil
}

// testHost answers test.js's __host calls.
func (h *Harness) testHost(log io.Writer) jsvm.Host {
	return func(ctx context.Context, op string, arg []byte) ([]byte, error) {
		switch op {
		case "mock.http":
			var a struct {
				Prefix  string            `json:"prefix"`
				Status  int               `json:"status"`
				Headers map[string]string `json:"headers"`
				Body    string            `json:"body"`
			}
			if err := json.Unmarshal(arg, &a); err != nil {
				return nil, err
			}
			h.mockHTTP(a.Prefix, plugin.FetchResponse{Status: a.Status, Headers: a.Headers, Body: a.Body})
			return []byte("null"), nil
		case "mock.api":
			var a struct {
				Method string          `json:"method"`
				Path   string          `json:"path"`
				Status int             `json:"status"`
				Body   json.RawMessage `json:"body"`
			}
			if err := json.Unmarshal(arg, &a); err != nil {
				return nil, err
			}
			h.mockAPI(a.Method, a.Path, a.Status, a.Body)
			return []byte("null"), nil
		case "mock.calls":
			return json.Marshal(h.recorded())
		case "mock.reset":
			h.resetMocks()
			return []byte("null"), nil
		case "plugin.call":
			var a struct {
				Name string          `json:"name"`
				Arg  json.RawMessage `json:"arg"`
			}
			if err := json.Unmarshal(arg, &a); err != nil {
				return nil, err
			}
			out, err := h.Call(ctx, a.Name, a.Arg)
			return out, jsDetail(err)
		case "plugin.event":
			var a struct {
				Event string          `json:"event"`
				Data  json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(arg, &a); err != nil {
				return nil, err
			}
			out, err := h.Event(ctx, a.Event, a.Data)
			return out, jsDetail(err)
		case "dev.op":
			var a struct {
				Op  string          `json:"op"`
				Arg json.RawMessage `json:"arg"`
			}
			if err := json.Unmarshal(arg, &a); err != nil {
				return nil, err
			}
			if !strings.HasPrefix(a.Op, "kv.") && !strings.HasPrefix(a.Op, "db.") {
				return nil, fmt.Errorf("plugin.%s is not available in tests", a.Op)
			}
			return h.Host.DevOp(ctx, h.ID, a.Op, a.Arg)
		case "log":
			if log != nil {
				var a struct {
					Msg string `json:"msg"`
				}
				_ = json.Unmarshal(arg, &a)
				fmt.Fprintf(log, "    · %s\n", a.Msg)
			}
			return []byte("null"), nil
		}
		return nil, fmt.Errorf("unknown operation %q", op)
	}
}

// jsDetail keeps a plugin's stack trace in the error a test sees.
func jsDetail(err error) error {
	var je *jsvm.JSError
	if errors.As(err, &je) && je.Stack != "" {
		return fmt.Errorf("%s\n%s", je.Message, strings.TrimSpace(je.Stack))
	}
	return err
}
