package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// The admin side of plugins (internal/plugin). Installing is two steps on purpose:
// the package is uploaded and inspected — the consent screen shows what it asks
// for — and only then installed, with the operator's password, against the exact
// package they looked at (its sha256 and what it asks for are part of the request).
// The bytes wait here between the two, so a 5 MB zip is not sent twice.

const pluginUploadTTL = 15 * time.Minute

type pluginUpload struct {
	raw []byte
	at  time.Time
}

// pluginUploads keeps inspected packages by sha256 until they are installed or go stale.
type pluginUploads struct {
	mu sync.Mutex
	m  map[string]pluginUpload
}

func (u *pluginUploads) put(sha string, raw []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.m == nil {
		u.m = map[string]pluginUpload{}
	}
	for k, v := range u.m {
		if time.Since(v.at) > pluginUploadTTL {
			delete(u.m, k)
		}
	}
	if len(u.m) >= 8 { // a handful of operators at most; never a store
		for k := range u.m {
			delete(u.m, k)
			break
		}
	}
	u.m[sha] = pluginUpload{raw: raw, at: time.Now()}
}

func (u *pluginUploads) take(sha string) ([]byte, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	v, ok := u.m[sha]
	if !ok || time.Since(v.at) > pluginUploadTTL {
		delete(u.m, sha)
		return nil, false
	}
	delete(u.m, sha)
	return v.raw, true
}

// pluginInspection is what the consent screen shows.
type pluginInspection struct {
	SHA256     string             `json:"sha256"`
	Size       int                `json:"size"`
	Manifest   *manifest.Manifest `json:"manifest"`
	Readme     string             `json:"readme,omitempty"`
	RiskyPerms []string           `json:"risky_perms,omitempty"`
	Exports    []string           `json:"exports"`
	// Installed is the version already installed under this id, for an update.
	Installed string `json:"installed,omitempty"`
	// Added lists what an update asks for beyond the installed version.
	AddedPerms []string `json:"added_perms,omitempty"`
	AddedNet   []string `json:"added_net,omitempty"`
}

func (rt *Router) pluginHost(w http.ResponseWriter) *plugin.Host {
	if rt.plugins == nil {
		writeErrCode(w, http.StatusServiceUnavailable, "err.internal", "внутренняя ошибка сервера")
		return nil
	}
	return rt.plugins
}

func (rt *Router) listPlugins(w http.ResponseWriter, _ *http.Request) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": h.List()})
}

// inspectPlugin takes a package as the request body (application/zip), or a URL
// to fetch it from ({"url": …}), and returns what the consent screen shows.
func (rt *Router) inspectPlugin(w http.ResponseWriter, r *http.Request) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	var raw []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var req struct {
			URL string `json:"url"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		b, err := fetchPluginPackage(r.Context(), req.URL)
		if err != nil {
			writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
			return
		}
		raw = b
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, manifest.MaxPackage+1)
		b, err := io.ReadAll(r.Body)
		if err != nil || len(b) > manifest.MaxPackage {
			writeErrCode(w, http.StatusRequestEntityTooLarge, "err.pluginTooLarge", "пакет больше 5 МБ")
			return
		}
		raw = b
	}
	rt.respondInspection(w, h, raw)
}

// respondInspection reads a package and answers with what the consent screen shows,
// keeping the bytes for the install.
func (rt *Router) respondInspection(w http.ResponseWriter, h *plugin.Host, raw []byte) {
	pkg, err := h.Inspect(raw)
	if err != nil {
		writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
		return
	}
	m := pkg.Manifest
	out := pluginInspection{
		SHA256: pkg.SHA256, Size: len(raw), Manifest: m, Readme: pkg.Readme, Exports: m.Exports(),
	}
	for _, p := range m.Permissions {
		if slices.Contains(manifest.RiskyPerms, p) {
			out.RiskyPerms = append(out.RiskyPerms, p)
		}
	}
	if cur, err := h.Get(m.ID); err == nil && cur.Manifest != nil {
		out.Installed = cur.Version
		for _, p := range m.Permissions {
			if !slices.Contains(cur.Manifest.Permissions, p) {
				out.AddedPerms = append(out.AddedPerms, p)
			}
		}
		for _, n := range m.Net {
			if !slices.Contains(cur.Manifest.Net, n) {
				out.AddedNet = append(out.AddedNet, n)
			}
		}
	}
	rt.pluginUploads.put(pkg.SHA256, raw)
	writeJSON(w, http.StatusOK, out)
}

// fetchPluginPackage downloads a package for "install by URL", through the same
// guarded fetcher plugins use: an admin's URL must not reach the box's own ports.
func fetchPluginPackage(ctx context.Context, raw string) ([]byte, error) {
	return fetchGuarded(ctx, raw, manifest.MaxPackage)
}

// fetchGuarded downloads an admin-given address, up to max bytes, through the
// plugins' guarded fetcher.
func fetchGuarded(ctx context.Context, raw string, max int) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, errors.New("enter an http(s) link")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := plugin.NewFetcher().Fetch(ctx, []string{strings.ToLower(u.Hostname())},
		plugin.FetchRequest{URL: u.String(), MaxBytes: max})
	if err != nil {
		return nil, err
	}
	if resp.Status != http.StatusOK {
		return nil, errors.New("the link answered " + http.StatusText(resp.Status))
	}
	return []byte(resp.Body), nil
}

// pluginInstallBody is the consent plus the password.
type pluginInstallBody struct {
	SHA256          string   `json:"sha256"`
	Perms           []string `json:"perms"`
	Net             []string `json:"net"`
	CurrentPassword string   `json:"current_password"`
}

func (rt *Router) installPlugin(w http.ResponseWriter, r *http.Request) {
	rt.installOrUpdatePlugin(w, r, "")
}

func (rt *Router) updatePlugin(w http.ResponseWriter, r *http.Request) {
	rt.installOrUpdatePlugin(w, r, r.PathValue("id"))
}

func (rt *Router) installOrUpdatePlugin(w http.ResponseWriter, r *http.Request, updateID string) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	var req pluginInstallBody
	if !decodeJSON(w, r, &req) {
		return
	}
	// An admin hands a plugin only what they hold themselves — the rule API keys
	// follow. Without it, installing a plugin would be a way to a permission the
	// role was never given (an admin without audit.view reading the journal through
	// a plugin that has it).
	caller := callerPerms(r)
	var beyond []string
	for p := range model.NewPermSet(req.Perms) {
		if !caller.Has(p) {
			beyond = append(beyond, p)
		}
	}
	if len(beyond) > 0 {
		slices.Sort(beyond)
		writeErrDetail(w, http.StatusForbidden, "err.pluginPermsBeyond", "у вас нет прав, которые просит плагин: ", strings.Join(beyond, ", "))
		return
	}
	if !rt.verifyStepUp(w, r, req.CurrentPassword) {
		return
	}
	raw, ok := rt.pluginUploads.take(req.SHA256)
	if !ok {
		writeErrCode(w, http.StatusGone, "err.pluginUploadExpired", "загрузка устарела — выберите файл ещё раз")
		return
	}
	consent := plugin.Consent{SHA256: req.SHA256, Perms: req.Perms, Net: req.Net}
	var info *plugin.Info
	var err error
	if updateID == "" {
		info, err = h.Install(r.Context(), raw, consent)
	} else {
		pkg, perr := h.Inspect(raw)
		if perr == nil && pkg.Manifest.ID != updateID {
			writeErrDetail(w, http.StatusBadRequest, "err.pluginWrongID", "это другой плагин: ", pkg.Manifest.ID)
			return
		}
		info, err = h.Update(r.Context(), raw, consent)
	}
	if err != nil {
		writePluginErr(w, err)
		return
	}
	// An update may bring onHttp or a payment method: their addresses need the
	// callback secret as much as on enable.
	rt.ensurePluginCallbacks(info)
	writeJSON(w, http.StatusOK, info)
}

func (rt *Router) pluginAction(fn func(*plugin.Host, context.Context, string) (*plugin.Info, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := rt.pluginHost(w)
		if h == nil {
			return
		}
		info, err := fn(h, r.Context(), r.PathValue("id"))
		if err != nil {
			writePluginErr(w, err)
			return
		}
		rt.ensurePluginCallbacks(info)
		writeJSON(w, http.StatusOK, info)
	}
}

func (rt *Router) configurePlugin(w http.ResponseWriter, r *http.Request) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	var req struct {
		Values map[string]string `json:"values"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	info, err := h.SetConfig(r.Context(), r.PathValue("id"), req.Values)
	var problems manifest.Problems
	if errors.As(err, &problems) {
		// A value the form refused, not the package: say so in those words.
		writeErrDetail(w, http.StatusBadRequest, "err.pluginSettings", "настройки не подходят: ", err.Error())
		return
	}
	if err != nil {
		writePluginErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (rt *Router) uninstallPlugin(w http.ResponseWriter, r *http.Request) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	keep := r.URL.Query().Get("keep_data") == "1"
	if err := h.Uninstall(r.Context(), r.PathValue("id"), keep); err != nil {
		writePluginErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *Router) pluginLogs(w http.ResponseWriter, r *http.Request) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	lines, err := h.Logs(r.PathValue("id"))
	if err != nil {
		writePluginErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func (rt *Router) pluginCode(w http.ResponseWriter, r *http.Request) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	code, err := h.Code(r.PathValue("id"))
	if err != nil {
		writePluginErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"code": code})
}

// writePluginErr maps the host's errors to the panel's codes.
func writePluginErr(w http.ResponseWriter, err error) {
	var problems manifest.Problems
	switch {
	case errors.Is(err, plugin.ErrNotFound):
		writeErrCode(w, http.StatusNotFound, "err.pluginNotFound", "плагин не установлен")
	case errors.Is(err, plugin.ErrExists):
		writeErrCode(w, http.StatusConflict, "err.pluginExists", "такой плагин уже установлен — обновите его")
	case errors.Is(err, plugin.ErrConsent):
		writeErrCode(w, http.StatusConflict, "err.pluginConsent", "пакет изменился после проверки — загрузите его ещё раз")
	case errors.As(err, &problems):
		writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
	default:
		writeErrDetail(w, http.StatusUnprocessableEntity, "err.pluginFailed", "плагин не запустился: ", err.Error())
	}
}

// handlePluginHTTP serves /<callback secret>/x/<plugin id>/<path> with the plugin's
// onHttp. An id that is not an active plugin accepting requests gets the decoy, like
// any unknown path behind this segment.
func (rt *Router) handlePluginHTTP(w http.ResponseWriter, r *http.Request, rest string, decoy http.Handler) {
	id, path := firstSegment(rest)
	if rt.plugins == nil || !rt.plugins.ServesHTTP(id) {
		decoy.ServeHTTP(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, plugin.MaxHTTPBody+1))
	if err != nil || len(body) > plugin.MaxHTTPBody {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	req := plugin.HTTPRequest{
		Method: r.Method, Path: path, Query: map[string]string{}, Headers: map[string]string{},
		Body: string(body), IP: clientIP(r),
	}
	for k, v := range r.URL.Query() {
		req.Query[k] = strings.Join(v, ",")
	}
	for k, v := range r.Header {
		if strings.EqualFold(k, "Cookie") { // the panel's own session never reaches a plugin
			continue
		}
		req.Headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	resp, err := rt.plugins.ServeHTTP(r.Context(), id, req)
	if err != nil {
		http.Error(w, "the plugin failed to answer", http.StatusBadGateway)
		return
	}
	plugin.WriteHTTP(w, resp)
}

// ensurePluginCallbacks makes the public callback segment exist once a plugin that
// is reached through it — onHttp or a payment method — is switched on, and fills
// in the URLs the operator gives the outside service.
func (rt *Router) ensurePluginCallbacks(info *plugin.Info) {
	if info == nil || info.Manifest == nil || info.Status != "active" {
		return
	}
	if !info.Manifest.Provides.HTTP && info.Manifest.Provides.Payment == nil {
		return
	}
	if err := rt.mgr.EnsureCallbackSecret(); err != nil {
		return
	}
	rt.setPaySecret(rt.mgr.PaymentWebhookSecret())
	if fresh, err := rt.plugins.Get(info.ID); err == nil {
		*info = *fresh
	}
}

// panelLang is the admin's language for what plugins label themselves with.
func panelLang(r *http.Request) string {
	if l := r.URL.Query().Get("lang"); l == "en" || l == "ru" {
		return l
	}
	return "ru"
}

func (rt *Router) listPluginActions(w http.ResponseWriter, r *http.Request) {
	if rt.plugins == nil {
		writeJSON(w, http.StatusOK, map[string]any{"actions": []any{}})
		return
	}
	held := callerPerms(r)
	actions := rt.plugins.Actions(panelLang(r), func(p string) bool { return held.Has(p) })
	if actions == nil {
		actions = []plugin.ActionInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions})
}

func (rt *Router) runPluginAction(w http.ResponseWriter, r *http.Request) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	var req struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	held := callerPerms(r)
	res, _, err := h.RunAction(r.Context(), r.PathValue("plugin"), r.PathValue("key"), req.UserIDs,
		func(p string) bool { return held.Has(p) })
	switch {
	case errors.Is(err, plugin.ErrForbidden):
		writeErrCode(w, http.StatusForbidden, "err.forbidden", "недостаточно прав")
	case errors.Is(err, plugin.ErrNoAction), errors.Is(err, plugin.ErrNotActive), errors.Is(err, plugin.ErrNotFound):
		writeErrCode(w, http.StatusNotFound, "err.pluginNotFound", "плагин не установлен")
	case err != nil:
		// The plugin ran and failed (or refused the selection): not a start failure.
		writeErrDetail(w, http.StatusUnprocessableEntity, "err.pluginActionFailed", "действие не выполнено: ", err.Error())
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

func (rt *Router) pluginWidgets(w http.ResponseWriter, r *http.Request) {
	widgets := []plugin.Widget{}
	if rt.plugins != nil {
		if ws := rt.plugins.Widgets(r.Context(), panelLang(r)); ws != nil {
			widgets = ws
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"widgets": widgets})
}

func (rt *Router) userPluginFields(w http.ResponseWriter, r *http.Request, id int64) {
	blocks := []plugin.PluginFields{}
	if rt.plugins != nil {
		if fs := rt.plugins.UserFields(r.Context(), id, panelLang(r)); fs != nil {
			blocks = fs
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": blocks})
}
