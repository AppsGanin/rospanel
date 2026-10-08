package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// onHttp: requests from the internet to /<callback secret>/x/<plugin id>/<path>,
// for services that call a URL of their own choosing — a shop selling access keys,
// a form on a website, an OAuth callback. The plugin authenticates the caller
// itself (a signature, a token in its settings).

const (
	HTTPTimeout     = 10 * time.Second
	MaxHTTPBody     = 1 << 20
	maxHTTPResponse = 1 << 20
)

// HTTPRequest is what onHttp receives.
type HTTPRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"` // after /x/<id>, starting with /
	Query   map[string]string `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	IP      string            `json:"ip"`
}

// HTTPResponse is what onHttp answers.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// ServesHTTP reports whether id is an active plugin that accepts requests.
func (h *Host) ServesHTTP(id string) bool {
	inst, err := h.get(id)
	if err != nil {
		return false
	}
	p := inst.pub.Load()
	return p != nil && p.status == model.PluginActive && p.manifest != nil && p.manifest.Provides.HTTP
}

// HTTPPlugins lists the active plugins that accept requests.
func (h *Host) HTTPPlugins() []string {
	return h.Active(func(m *manifest.Manifest) bool { return m.Provides.HTTP })
}

// ServeHTTP calls a plugin's onHttp.
func (h *Host) ServeHTTP(ctx context.Context, id string, req HTTPRequest) (*HTTPResponse, error) {
	out, err := h.call(ctx, id, "onHttp", req, HTTPTimeout, callOpts{public: true})
	if err != nil {
		return nil, err
	}
	var resp HTTPResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("onHttp returned %s, not {status, headers, body}", firstLine(string(out)))
	}
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	if resp.Status < 200 || resp.Status > 599 {
		return nil, fmt.Errorf("onHttp: status %d", resp.Status)
	}
	if len(resp.Body) > maxHTTPResponse {
		return nil, fmt.Errorf("onHttp: answer over %d KB", maxHTTPResponse>>10)
	}
	return &resp, nil
}

// WriteHTTP writes a plugin's answer, making it safe to serve from the panel's own
// origin: the admin panel lives on this host too, so a page a plugin returns must
// not be able to run script with the panel's cookies. CSP sandbox gives the answer
// an opaque origin, nosniff stops a "text/plain" being run as anything else, and a
// plugin may not set cookies here at all.
func WriteHTTP(w http.ResponseWriter, resp *HTTPResponse) {
	hdr := w.Header()
	for k, v := range resp.Headers {
		switch strings.ToLower(k) {
		case "set-cookie", "content-security-policy", "content-security-policy-report-only",
			"x-content-type-options", "strict-transport-security", "content-length", "transfer-encoding", "connection",
			// The panel's origin is the admin's too: no wiping their session, no
			// refresh-redirects, no service workers, no cross-origin grants.
			"clear-site-data", "refresh", "service-worker-allowed", "link",
			"access-control-allow-origin", "access-control-allow-credentials", "access-control-allow-headers", "access-control-allow-methods":
			continue
		}
		if strings.ContainsAny(k+v, "\r\n") {
			continue
		}
		hdr.Set(k, v)
	}
	// No script from the panel's origin: something a <script src> could load there
	// is served as text.
	if ct := strings.ToLower(hdr.Get("Content-Type")); ct == "" || strings.Contains(ct, "javascript") || strings.Contains(ct, "ecmascript") {
		hdr.Set("Content-Type", "text/plain; charset=utf-8")
	}
	hdr.Set("Content-Security-Policy", "sandbox")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(resp.Status)
	_, _ = w.Write([]byte(resp.Body))
}
