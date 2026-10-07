package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/AppsGanin/rospanel/internal/plugin"
	"github.com/AppsGanin/rospanel/internal/plugin/catalog"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
	"github.com/AppsGanin/rospanel/internal/version"
)

// The plugin catalog (internal/plugin/catalog) in the admin panel: the list, the
// install and update from it — through the same consent screen as a zip, with
// the package checked against the catalog's sha256 — and the operator told once
// about each new version. Nothing updates by itself.

// pluginCatalog is the Router's catalog, made on first use.
type pluginCatalog struct {
	once sync.Once
	c    *catalog.Catalog
	// fetch and keys replace the guarded fetcher and the built-in keys in tests.
	fetch catalog.Fetch
	keys  []string
}

func (rt *Router) catalog() *catalog.Catalog {
	pc := &rt.pluginCatalog
	pc.once.Do(func() {
		pc.c = catalog.New(rt.catalogFetch, filepath.Join(rt.dataDir, "cache", "catalog"), pc.keys...)
	})
	return pc.c
}

// catalogFetch downloads the catalog's files through the guarded fetcher.
func (rt *Router) catalogFetch(ctx context.Context, u string, max int) ([]byte, error) {
	if f := rt.pluginCatalog.fetch; f != nil {
		return f(ctx, u, max)
	}
	return fetchGuarded(ctx, u, max)
}

// catalogItem is one plugin as the catalog page lists it.
type catalogItem struct {
	ID          string        `json:"id"`
	Name        manifest.Text `json:"name"`
	Description manifest.Text `json:"description,omitempty"`
	Author      string        `json:"author,omitempty"`
	Homepage    string        `json:"homepage,omitempty"`
	// Latest is the newest version that runs on this panel; Newest the newest at
	// all (it may need a newer panel).
	Latest *catalog.Version `json:"latest,omitempty"`
	Newest string           `json:"newest"`
	// Installed is the version installed here; InstalledVerified whether that very
	// package is a reviewed one; Update whether Latest is newer.
	Installed         string `json:"installed,omitempty"`
	InstalledVerified bool   `json:"installed_verified,omitempty"`
	Update            bool   `json:"update,omitempty"`
}

func (rt *Router) catalogState(ctx context.Context, refresh bool) (catalog.State, bool, error) {
	custom, err := rt.mgr.Store().PluginCatalogURL()
	if err != nil {
		return catalog.State{}, false, err
	}
	return rt.catalog().Get(ctx, custom, refresh), custom != "", nil
}

func (rt *Router) getPluginCatalog(w http.ResponseWriter, r *http.Request) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	st, custom, err := rt.catalogState(r.Context(), r.URL.Query().Get("refresh") != "")
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return
	}
	items := []catalogItem{}
	if st.Index != nil {
		for i := range st.Index.Plugins {
			items = append(items, catalogView(h, &st.Index.Plugins[i]))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url": st.URL, "custom": custom, "fetched_at": st.FetchedAt, "error": st.Error, "plugins": items,
	})
}

func catalogView(h *plugin.Host, e *catalog.Entry) catalogItem {
	it := catalogItem{ID: e.ID, Name: e.Name, Description: e.Description, Author: e.Author, Homepage: e.Homepage,
		Latest: e.Latest(version.Version)}
	if len(e.Versions) > 0 {
		it.Newest = e.Versions[0].Version
	}
	if cur, err := h.Get(e.ID); err == nil {
		it.Installed = cur.Version
		if v := e.Find(cur.Version); v != nil && v.SHA256 == cur.SHA256 {
			it.InstalledVerified = v.Verified
		}
		it.Update = it.Latest != nil && manifest.VersionLess(cur.Version, it.Latest.Version)
	}
	return it
}

// setPluginCatalog sets the catalog's address: "" for the official one, or a
// mirror (still signed by the official key).
func (rt *Router) setPluginCatalog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	u := strings.TrimSpace(req.URL)
	if u == catalog.DefaultURL {
		u = ""
	}
	if u != "" {
		p, err := url.Parse(u)
		if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" || len(u) > 1000 {
			writeErrCode(w, http.StatusBadRequest, "err.pluginCatalogURL", "адрес каталога — ссылка http(s) на index.json")
			return
		}
	}
	if err := rt.mgr.Store().SetPluginCatalogURL(u); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return
	}
	rt.getPluginCatalog(w, r)
}

// inspectCatalogPlugin downloads a version from the catalog, checks it is the
// package the signed index names, and answers with the consent screen.
func (rt *Router) inspectCatalogPlugin(w http.ResponseWriter, r *http.Request) {
	h := rt.pluginHost(w)
	if h == nil {
		return
	}
	var req struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	st, _, err := rt.catalogState(r.Context(), false)
	if err != nil {
		writeErrCode(w, http.StatusInternalServerError, "err.internal", "внутренняя ошибка сервера")
		return
	}
	e := st.Index.Lookup(req.ID)
	var v *catalog.Version
	if e != nil {
		if req.Version == "" {
			v = e.Latest(version.Version)
		} else {
			v = e.Find(req.Version)
		}
	}
	if v == nil {
		writeErrCode(w, http.StatusNotFound, "err.pluginNotInCatalog", "в каталоге нет такой версии")
		return
	}
	link, err := catalog.PackageURL(st.URL, v)
	if err != nil {
		writeErrDetail(w, http.StatusBadRequest, "err.pluginInvalid", "пакет не подходит: ", err.Error())
		return
	}
	raw, err := rt.catalogFetch(r.Context(), link, manifest.MaxPackage)
	if err != nil {
		writeErrDetail(w, http.StatusBadGateway, "err.pluginCatalogFetch", "каталог недоступен: ", err.Error())
		return
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != v.SHA256 {
		writeErrCode(w, http.StatusBadGateway, "err.pluginCatalogMismatch", "пакет не совпадает с каталогом")
		return
	}
	rt.respondInspection(w, h, raw, func(in *pluginInspection) {
		in.FromCatalog, in.Verified = true, v.Verified
	})
}

// CheckPluginUpdates tells the operator once about each new catalog version of an
// installed plugin (run by the panel every few hours). Nothing is installed.
func (rt *Router) CheckPluginUpdates(ctx context.Context) {
	h := rt.plugins
	if h == nil {
		return
	}
	st, _, err := rt.catalogState(ctx, false)
	if err != nil || st.Index == nil {
		return
	}
	notified, err := rt.mgr.Store().PluginUpdatesNotified()
	if err != nil {
		return
	}
	changed := false
	for _, info := range h.List() {
		e := st.Index.Lookup(info.ID)
		if e == nil {
			continue
		}
		it := catalogView(h, e)
		if !it.Update || notified[info.ID] == it.Latest.Version {
			continue
		}
		name := info.ID
		if info.Manifest != nil {
			name = info.Manifest.Name.Get(string(rt.mgr.BotLang()))
		}
		rt.mgr.NotifyPluginUpdate(name, info.Version, it.Latest.Version)
		notified[info.ID] = it.Latest.Version
		changed = true
	}
	if changed {
		_ = rt.mgr.Store().SetPluginUpdatesNotified(notified)
	}
}
