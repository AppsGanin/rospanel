// Package decoy serves an innocent-looking website ("заглушка") for every
// request that doesn't carry the secret panel path. The goal: a visitor, DPI,
// or scanner sees an ordinary site, never a hint that a VPN panel exists.
package decoy

import (
	"embed"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:templates
var templatesFS embed.FS

// Available returns the list of bundled template slugs.
func Available() ([]string, error) {
	entries, err := fs.ReadDir(templatesFS, "templates")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Handler serves a single decoy template.
type Handler struct {
	fsys   fs.FS
	status int // status for the HTML page: 200, or 503 for maintenance templates
}

// maintenanceTemplates are slugs whose front page advertises the site as
// temporarily unavailable; they should answer with 503, not 200, so the body and
// status agree (a "503" page served as 200 is an easy tell).
var maintenanceTemplates = map[string]bool{"503-1": true, "503-2": true, "maintenance": true}

// New returns a decoy handler for the given template slug.
func New(template string) (*Handler, error) {
	if template == "" {
		template = "coming-soon"
	}
	sub, err := fs.Sub(templatesFS, path.Join("templates", template))
	if err != nil {
		return nil, fmt.Errorf("decoy template %q: %w", template, err)
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, fmt.Errorf("decoy template %q missing index.html: %w", template, err)
	}
	status := http.StatusOK
	if maintenanceTemplates[template] {
		status = http.StatusServiceUnavailable
	}
	return &Handler{fsys: sub, status: status}, nil
}

// ServeHTTP serves the requested asset, falling back to the template's 404.html
// (with a 404 status) for anything not found — so misses are indistinguishable
// from a normal site's not-found page.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Look like a stock nginx box.
	w.Header().Set("Server", "nginx")

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}

	body, err := fs.ReadFile(h.fsys, name)
	if err != nil {
		h.serveNotFound(w)
		return
	}
	w.Header().Set("Content-Type", contentType(name))
	w.WriteHeader(h.pageStatus(name))
	_, _ = w.Write(body)
}

// pageStatus returns the status for a served file: a maintenance/503 decoy returns
// 503 for its HTML page (assets stay 200 so the page still renders styled); every
// other template returns 200.
func (h *Handler) pageStatus(name string) int {
	if h.status != http.StatusOK && strings.HasSuffix(name, ".html") {
		return h.status
	}
	return http.StatusOK
}

// serveNotFound answers an unknown path. A maintenance/503 decoy serves its
// unavailable page (503); any other template serves its own 404.html, or falls
// back to its index page — far less of a tell than a stock-nginx 404 body sitting
// under a custom-looking site. The hardcoded nginx body is a last resort that
// real templates (which all ship index.html) never reach.
func (h *Handler) serveNotFound(w http.ResponseWriter) {
	status := http.StatusNotFound
	candidates := []string{"404.html", "index.html"}
	if h.status == http.StatusServiceUnavailable {
		status, candidates = http.StatusServiceUnavailable, []string{"index.html"}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	for _, name := range candidates {
		if body, err := fs.ReadFile(h.fsys, name); err == nil {
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte("<html><head><title>404 Not Found</title></head><body><center><h1>404 Not Found</h1></center><hr><center>nginx</center></body></html>"))
}

func contentType(name string) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "text/html; charset=utf-8"
}
