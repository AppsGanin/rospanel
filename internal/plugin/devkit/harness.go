package devkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/plugin/jsvm"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// Harness runs one plugin on the real host, against a temporary directory, with
// panel.api and panel.http.fetch answered by mocks — or by a real panel, for dev.
type Harness struct {
	Host *plugin.Host
	ID   string
	dir  string

	mu    sync.Mutex
	http  []httpMock
	mocks []apiMock
	calls []Call
	api   plugin.APICaller // answers what no mock does (Options.API)

	// Remote, when set, answers panel.api for real (dev against a panel).
	Remote *Remote
	// RealHTTP sends unmocked fetches out for real (dev); tests refuse them.
	RealHTTP bool
	fetcher  *plugin.HTTPFetcher
}

// Remote is a panel's /v1, reached with an API key.
type Remote struct {
	Base string // https://host/<api path> — the part before /v1
	Key  string
}

type httpMock struct {
	prefix string
	resp   plugin.FetchResponse
}

type apiMock struct {
	method, path string
	status       int
	body         []byte
}

// Call is one request the plugin made, for mock.calls().
type Call struct {
	Kind   string `json:"kind"`
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

// Options start a harness.
type Options struct {
	Settings     map[string]string
	PanelVersion string
	Log          io.Writer // the plugin's log lines as they come; nil discards
	// Engine, when set, is a running panel's: its compiled guest is reused.
	Engine *jsvm.Engine
	// API, when set, answers panel.api calls no mock answers (a trial run in the
	// panel), in place of the "add a mock" refusal.
	API plugin.APICaller
}

// Start installs and enables the package on a fresh host under a temporary
// directory. Close removes it.
func Start(ctx context.Context, raw []byte, o Options) (*Harness, error) {
	dir, err := os.MkdirTemp("", "rospanel-plugin-dev-")
	if err != nil {
		return nil, err
	}
	h := &Harness{dir: dir, fetcher: plugin.NewFetcher(), api: o.API}
	lg := slog.New(slog.DiscardHandler)
	if o.Log != nil {
		lg = slog.New(slog.NewTextHandler(o.Log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	h.Host = plugin.New(plugin.Deps{
		Store: newMemStore(), DataDir: dir, PanelVersion: o.PanelVersion,
		API: h.serveAPI, Fetch: h, Logger: lg, NoBreaker: true, Engine: o.Engine,
	})
	if err := h.load(ctx, raw, o.Settings, false); err != nil {
		h.Close()
		return nil, err
	}
	return h, nil
}

// load installs (or, with update, replaces) the package and enables it.
func (h *Harness) load(ctx context.Context, raw []byte, settings map[string]string, update bool) error {
	pkg, err := h.Host.Inspect(raw)
	if err != nil {
		return err
	}
	consent := plugin.Consent{SHA256: pkg.SHA256, Perms: pkg.Manifest.Permissions, Net: pkg.Manifest.Net}
	if update {
		if _, err := h.Host.Update(ctx, raw, consent); err != nil {
			return err
		}
	} else {
		if _, err := h.Host.Install(ctx, raw, consent); err != nil {
			return err
		}
		h.ID = pkg.Manifest.ID
		// A key the plugin has no setting for is left out, not refused: a field the
		// manifest dropped must not stop every test from running.
		settings = knownSettings(pkg.Manifest, settings)
		if len(settings) > 0 {
			if _, err := h.Host.SetConfig(ctx, h.ID, settings); err != nil {
				return fmt.Errorf("dev.config.json settings: %w", err)
			}
		}
	}
	info, err := h.Host.Enable(ctx, h.ID)
	if err != nil {
		return err
	}
	if info.Status != model.PluginActive {
		return fmt.Errorf("the plugin did not start: %s", info.StatusError)
	}
	return nil
}

// Reload replaces the running code with a new package, keeping the database.
func (h *Harness) Reload(ctx context.Context, raw []byte) error {
	return h.load(ctx, raw, nil, true)
}

// Close stops the host and removes the temporary directory.
func (h *Harness) Close() {
	_ = h.Host.Close(context.Background())
	_ = os.RemoveAll(h.dir)
}

// Call invokes an export with a generous deadline.
func (h *Harness) Call(ctx context.Context, export string, arg any) (json.RawMessage, error) {
	return h.Host.Call(ctx, h.ID, export, arg, 30*time.Second)
}

// Event delivers an event to onEvent the way the panel's outbox would.
func (h *Harness) Event(ctx context.Context, event string, data json.RawMessage) (json.RawMessage, error) {
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	payload := map[string]any{
		"id": fmt.Sprintf("dev-%d", time.Now().UnixNano()), "event": event,
		"created_at": time.Now().Unix(), "data": data,
	}
	return h.Call(ctx, "onEvent", payload)
}

// Logs returns the plugin's log.
func (h *Harness) Logs() []plugin.LogLine {
	lines, _ := h.Host.Logs(h.ID)
	return lines
}

// --- mocks ---

func (h *Harness) mockHTTP(prefix string, resp plugin.FetchResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.http = append([]httpMock{{prefix, resp}}, h.http...) // the latest wins
}

func (h *Harness) mockAPI(method, path string, status int, body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mocks = append([]apiMock{{strings.ToUpper(method), path, status, body}}, h.mocks...)
}

func (h *Harness) resetMocks() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.http, h.mocks, h.calls = nil, nil, nil
}

// Calls lists the requests the plugin made: to the internet and to panel.api.
func (h *Harness) Calls() []Call { return h.recorded() }

func (h *Harness) recorded() []Call {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Call{}, h.calls...) // [] rather than null in JS
}

func (h *Harness) record(c Call) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, c)
}

// Fetch serves panel.http.fetch: a mock when one matches, the allowlist always.
func (h *Harness) Fetch(ctx context.Context, allow []string, req plugin.FetchRequest) (*plugin.FetchResponse, error) {
	recorded := req.Body
	if req.BodyReader != nil && !h.RealHTTP {
		// A blob or a form: what was sent is what the test checks (the first MB of it).
		b, err := io.ReadAll(req.BodyReader)
		if err != nil {
			return nil, err
		}
		recorded = string(b[:min(len(b), 1<<20)])
		req.BodyReader, req.BodySize = bytes.NewReader(b), int64(len(b))
	}
	h.record(Call{Kind: "http", Method: strings.ToUpper(req.Method), URL: req.URL, Body: recorded})
	h.mu.Lock()
	var hit *plugin.FetchResponse
	for _, m := range h.http {
		if strings.HasPrefix(req.URL, m.prefix) {
			r := m.resp
			hit = &r
			break
		}
	}
	h.mu.Unlock()
	if hit != nil {
		// The allowlist holds for mocks too: a test must not pass on a host the
		// panel would refuse.
		if err := plugin.Allowed(req.URL, allow); err != nil {
			return nil, err
		}
		if req.Sink != nil {
			if _, err := io.WriteString(req.Sink, hit.Body); err != nil {
				return nil, err
			}
			hit.Body = ""
		}
		return hit, nil
	}
	if !h.RealHTTP {
		return nil, fmt.Errorf("no mock for %s %s — add mock.http(%q, {...}) to the test", req.Method, req.URL, req.URL)
	}
	return h.fetcher.Fetch(ctx, allow, req)
}

// serveAPI serves panel.api: a mock when one matches, else the remote panel, else
// a refusal that says how to mock it.
func (h *Harness) serveAPI(ctx context.Context, req plugin.APIRequest) (int, []byte, error) {
	h.record(Call{Kind: "api", Method: req.Method, URL: req.Path, Body: string(req.Body)})
	h.mu.Lock()
	var hit *apiMock
	for i, m := range h.mocks {
		if m.method == req.Method && (m.path == req.Path || (strings.HasSuffix(m.path, "*") && strings.HasPrefix(req.Path, strings.TrimSuffix(m.path, "*")))) {
			hit = &h.mocks[i]
			break
		}
	}
	h.mu.Unlock()
	if hit != nil {
		if req.Sink != nil {
			_, err := req.Sink.Write(hit.body)
			return hit.status, nil, err
		}
		return hit.status, hit.body, nil
	}
	if h.Remote != nil {
		return h.Remote.call(ctx, req)
	}
	if h.api != nil {
		return h.api(ctx, req)
	}
	body, _ := json.Marshal(map[string]string{
		"error": fmt.Sprintf("panel.api is mocked here: add mock.api(%q, %q, {...}) to the test, or run dev with --panel and --key", req.Method, req.Path),
	})
	return http.StatusNotImplemented, body, nil
}

func (r *Remote) call(ctx context.Context, req plugin.APIRequest) (int, []byte, error) {
	var body io.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}
	hr, err := http.NewRequestWithContext(ctx, req.Method, strings.TrimRight(r.Base, "/")+req.Path, body)
	if err != nil {
		return 0, nil, err
	}
	hr.Header.Set("Authorization", "Bearer "+r.Key)
	if req.Body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	if req.Sink != nil {
		_, err := io.Copy(req.Sink, resp.Body)
		return resp.StatusCode, nil, err
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, b, err
}

// --- the host's store, in memory ---

type memStore struct {
	mu sync.Mutex
	m  map[string]model.Plugin
}

func newMemStore() *memStore { return &memStore{m: map[string]model.Plugin{}} }

func (s *memStore) ListPlugins() ([]model.Plugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Plugin
	for _, p := range s.m {
		out = append(out, p)
	}
	return out, nil
}

func (s *memStore) GetPlugin(id string) (*model.Plugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.m[id]; ok {
		return &p, nil
	}
	return nil, nil
}

func (s *memStore) SavePlugin(p model.Plugin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[p.ID] = p
	return nil
}

func (s *memStore) SetPluginState(id string, enabled bool, status, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.m[id]
	p.Enabled, p.Status, p.StatusError = enabled, status, msg
	s.m[id] = p
	return nil
}

func (s *memStore) SetPluginConfig(id string, cfg map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.m[id]
	p.Config = cfg
	s.m[id] = p
	return nil
}

func (s *memStore) DeletePlugin(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

// DevConfig is dev.config.json: settings for test and dev runs.
type DevConfig struct {
	Settings map[string]string `json:"settings"`
}

// ReadDevConfig reads dev.config.json from a plugin directory, if there is one.
func ReadDevConfig(dir string) (DevConfig, error) {
	var c DevConfig
	b, err := os.ReadFile(filepath.Join(dir, "dev.config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("dev.config.json: %w", err)
	}
	return c, nil
}

func fetchResponse(status int, body string) plugin.FetchResponse {
	return plugin.FetchResponse{Status: status, Headers: map[string]string{}, Body: body}
}

// knownSettings keeps the values of settings the manifest declares.
func knownSettings(m *manifest.Manifest, settings map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range m.Settings {
		if v, ok := settings[f.Key]; ok {
			out[f.Key] = v
		}
	}
	return out
}
