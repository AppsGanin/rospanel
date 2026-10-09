package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/plugin"
)

// A theme plugin's stylesheet is linked from the page and its files are served by
// the panel, inert; without an active theme the address is the decoy.
func TestPluginThemeOnSubscriptionPage(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	rt := h.(*Router)
	u, err := mgr.CreateUser(t.Context(), "themed", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	host := plugin.New(plugin.Deps{Store: st, DataDir: rt.dataDir, PanelVersion: "4.4.0", Logger: slog.New(slog.DiscardHandler)})
	rt.SetPlugins(host)
	t.Cleanup(func() { _ = host.Close(context.Background()) })

	get := func(path string, browser bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", "curl/8")
		if browser {
			req.Header.Set("User-Agent", "Mozilla/5.0")
			req.Header.Set("Accept", "text/html")
		}
		req.RemoteAddr = testClientIP + ":40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	base := "/sub/" + u.SubToken
	if body := get(base, true).Body.String(); strings.Contains(body, "/theme/theme.css") {
		t.Fatal("a theme link without a theme")
	}
	if rec := get(base+"/theme/theme.css", false); strings.Contains(rec.Body.String(), "--bg") {
		t.Fatal("a theme file served without a theme")
	}

	raw := pluginZip(t, map[string]string{
		"plugin.json":     `{"id": "dark", "version": "1.0.0", "api": 1, "name": "Dark", "provides": {"theme": true}}`,
		"main.js":         `export {};`,
		"theme/theme.css": `:root { --bg: #000; } body { background-image: url(bg.svg); }`,
		"theme/bg.svg":    `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
	})
	pkg, err := host.Inspect(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Install(context.Background(), raw, plugin.Consent{SHA256: pkg.SHA256}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Enable(context.Background(), "dark"); err != nil {
		t.Fatal(err)
	}

	pageRec := get(base, true)
	if csp := pageRec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self'") || !strings.Contains(csp, "font-src 'self'") {
		t.Fatalf("the page's CSP: %q", csp)
	}
	page := pageRec.Body.String()
	if !strings.Contains(page, `/theme/theme.css?v=`+pkg.SHA256[:12]) {
		t.Fatalf("the page does not link the theme")
	}
	css := get(base+"/theme/theme.css", false)
	if css.Code != 200 || !strings.Contains(css.Body.String(), "--bg: #000") || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("theme.css: %d %q %q", css.Code, css.Header().Get("Content-Type"), css.Body.String())
	}
	svg := get(base+"/theme/bg.svg", false)
	if svg.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(svg.Header().Get("Content-Security-Policy"), "sandbox") ||
		svg.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("svg headers: %v", svg.Header())
	}
	if rec := get(base+"/theme/main.js", false); strings.Contains(rec.Body.String(), "export") {
		t.Fatal("a file outside theme/ was served")
	}
}
