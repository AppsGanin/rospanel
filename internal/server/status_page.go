package server

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/branding"
	"github.com/Shu1t3/rospanel-shu1t3/internal/core"
	"github.com/Shu1t3/rospanel-shu1t3/internal/i18n"
	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/status"
	"github.com/Shu1t3/rospanel-shu1t3/internal/sub"
)

// statusWindowDays is how much history the public page shows. Ninety days is the
// convention every status page follows, and it matches what the rollup keeps.
const statusWindowDays = 90

// handleStatus serves the public status page. Reached only when the operator has
// switched it on and the request's first segment matches the configured path —
// otherwise the decoy answers, exactly as it does for any unknown path.
func handleStatus(rt *Router, w http.ResponseWriter, r *http.Request, rest string) {
	// The page and its logo, and nothing else. Letting /<path>/anything render the
	// page would hand a scanner an endless supply of distinct URLs that all confirm
	// the panel, so every other leaf falls through to the decoy.
	leaf := strings.Trim(rest, "/")
	if leaf != "" && leaf != statusLogoFile {
		rt.currentDecoy().ServeHTTP(w, r)
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil || !set.StatusEnabled {
		rt.currentDecoy().ServeHTTP(w, r)
		return
	}
	if leaf == statusLogoFile {
		rt.serveStatusLogo(w)
		return
	}
	lang := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
	body, err := rt.statusBody(set, lang)
	if err != nil {
		// Same rule as the subscription surface: never 500 in public. A real site
		// wouldn't, and the error text would confirm what is running here.
		rt.currentDecoy().ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Short and shared: an outage banner that lags a minute is fine, and a page
	// linked in a support message can be opened by a crowd at once.
	w.Header().Set("Cache-Control", "public, max-age=30")
	w.Header().Set("X-Robots-Tag", "noindex")
	_, _ = w.Write(body)
}

// statusLogoFile is the one sub-resource the status path serves: the operator's own
// mark, so the page looks like their service rather than like a stock template.
const statusLogoFile = "logo.svg"

// serveStatusLogo hands over the branding logo, falling back to the built-in mark
// exactly as the subscription page does.
func (rt *Router) serveStatusLogo(w http.ResponseWriter) {
	b, err := branding.ReadLogo(rt.dataDir)
	if err != nil {
		b = sub.Logo()
	}
	w.Header().Set("Content-Type", branding.LogoContentType(b))
	// Public and short-lived: the same bytes for every visitor, and a logo changed in
	// the panel should show up on the status page within minutes, not on next reboot.
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(b)
}

// statusData converts the manager's report into the page's view model.
func statusData(rep *core.StatusReport, set *model.Settings, lang i18n.Lang) status.Data {
	// Absolute, not relative: the page answers at /<path> with no trailing slash, so
	// a bare "logo.svg" would resolve against the site root and 404 into the decoy.
	d := status.Data{
		HeadlineOK: rep.AllUp,
		LogoURL:    "/" + set.StatusPathOr() + "/" + statusLogoFile,
	}
	d.Theme(set.PanelName, set.PanelTheme, lang, rep.Days)
	for _, s := range rep.Servers {
		row := status.Server{Name: s.Name, Up: s.Up}
		if s.Samples > 0 {
			row.Uptime = status.FormatUptime(s.Uptime)
		}
		for _, day := range s.Days {
			row.Days = append(row.Days, status.Day{
				Class: status.DayClass(day.Samples, day.Ratio),
				Title: dayTitle(day, lang),
			})
		}
		d.Servers = append(d.Servers, row)
	}
	d.UpdatedAt = rep.At.Format("2006-01-02 15:04 MST")
	return d
}

// dayTitle is the tooltip on one bar: the date and what happened that day.
func dayTitle(day core.StatusDay, lang i18n.Lang) string {
	if day.Samples == 0 {
		return i18n.T(lang, "status.dayNone", day.Day)
	}
	pct := status.FormatUptime(day.Ratio * 100)
	switch status.DayClass(day.Samples, day.Ratio) {
	case "up":
		return i18n.T(lang, "status.dayUp", day.Day)
	case "partial":
		return i18n.T(lang, "status.dayPartial", day.Day, pct)
	default:
		return i18n.T(lang, "status.dayDown", day.Day, pct)
	}
}

// getStatusPage returns the status page settings for the panel.
func (rt *Router) getStatusPage(w http.ResponseWriter, _ *http.Request) {
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": set.StatusEnabled,
		"path":    set.StatusPathOr(),
	})
}

// saveStatusPage turns the public page on/off and sets its URL segment.
func (rt *Router) saveStatusPage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool   `json:"enabled"`
		Path    string `json:"path"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	set, err := rt.mgr.Settings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	path := strings.TrimSpace(strings.Trim(req.Path, "/"))
	if path == "" {
		path = "status"
	}
	if code, msg := statusPathProblem(path, set); code != "" {
		writeErrCode(w, http.StatusBadRequest, code, msg)
		return
	}
	if err := rt.mgr.Store().SetStatusPage(req.Enabled, path); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.setStatusPath(statusPathOf(req.Enabled, path))
	log.Printf("status page: enabled=%v path=%q", req.Enabled, path)
	writeOK(w)
}

// statusPathProblem validates the requested segment, returning an error code and a
// fallback message, or ("", "") when the path is usable.
//
// The collision checks are the point: every public surface is routed by its first
// segment, and a status path equal to the subscription prefix or the panel secret
// would shadow it — the panel would answer the status page at the URL an operator
// signs in at, or worse, hand strangers the segment they are meant never to learn.
func statusPathProblem(path string, set *model.Settings) (string, string) {
	if !validPathSegment(path) {
		return "err.badStatusPath", "путь может содержать только латиницу, цифры, дефис и подчёркивание"
	}
	switch path {
	case set.PanelSecretPath, set.SubPathOr(), set.APIPath, set.NodeAPIPath:
		return "err.statusPathTaken", "этот путь уже занят другой поверхностью панели"
	}
	return "", ""
}

// validPathSegment allows the same character set the subscription path does: a bare
// URL segment with nothing to escape.
func validPathSegment(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// statusPathOf is the segment the router should answer on: empty while the page is
// off, so a disabled page is not merely hidden but unrouted.
func statusPathOf(enabled bool, path string) string {
	if !enabled {
		return ""
	}
	if path == "" {
		return "status"
	}
	return path
}

// statusBody renders the status page, memoized per language for statusCacheTTL.
//
// This is the one surface a caller reaches holding NOTHING — no token, no session — and
// each render costs a settings read, a node list and a 90-day uptime scan across every
// server, against the single SQLite connection every panel request, node sync and stats
// write queues behind. The Cache-Control header buys nothing here (Xray terminates TLS
// itself; there is no CDN in front), so the cache has to live on this side. A page that
// lags half a minute is exactly what the header already promises.
func (rt *Router) statusBody(set *model.Settings, lang i18n.Lang) ([]byte, error) {
	rt.statusMu.Lock()
	defer rt.statusMu.Unlock()
	if c, ok := rt.statusCache[string(lang)]; ok && time.Since(c.at) < statusCacheTTL {
		return c.body, nil
	}
	rep, err := rt.mgr.StatusPageData(statusWindowDays)
	if err != nil {
		return nil, err
	}
	body, err := status.Render(statusData(rep, set, lang))
	if err != nil {
		return nil, err
	}
	if rt.statusCache == nil {
		rt.statusCache = map[string]statusPageCache{}
	}
	rt.statusCache[string(lang)] = statusPageCache{body: body, at: time.Now()}
	return body, nil
}

// statusCacheTTL matches the Cache-Control max-age the page advertises.
const statusCacheTTL = 30 * time.Second

type statusPageCache struct {
	body []byte
	at   time.Time
}
