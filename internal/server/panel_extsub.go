package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/model"
)

// External subscriptions: the sources and the servers read from them, for the
// Servers page. Everything here is master-only — the servers are handed to users
// in their subscriptions, which the master serves.

// listExternal answers with every source and every server, so the page renders
// the whole picture from one request.
func (rt *Router) listExternal(w http.ResponseWriter, _ *http.Request) {
	subs, err := rt.mgr.ExtSubscriptions()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	servers, err := rt.mgr.ExtServers()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if subs == nil {
		subs = []model.ExtSubscription{}
	}
	if servers == nil {
		servers = []model.ExtServer{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": subs, "servers": servers})
}

// createExternal stores a source and reads it once; the answer carries the source
// as stored and what the read found.
func (rt *Router) createExternal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sub, report, err := rt.mgr.CreateExtSubscription(r.Context(), req.Name, req.Source)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"id": sub.ID, "name": sub.Name, "servers": report.Total})
	writeJSON(w, http.StatusOK, map[string]any{"subscription": sub, "report": report})
}

func (rt *Router) deleteExternal(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.DeleteExtSubscription(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"id": id})
	writeOK(w)
}

func (rt *Router) syncExternal(w http.ResponseWriter, r *http.Request, id int64) {
	report, err := rt.mgr.SyncExtSubscription(r.Context(), id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"id": id, "servers": report.Total})
	writeJSON(w, http.StatusOK, report)
}

// enabledBody is the one-field body of every on/off route here.
func enabledBody(w http.ResponseWriter, r *http.Request) (bool, bool) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return false, false
	}
	return req.Enabled, true
}

func (rt *Router) setExternalEnabled(w http.ResponseWriter, r *http.Request, id int64) {
	enabled, ok := enabledBody(w, r)
	if !ok {
		return
	}
	if err := rt.mgr.SetExtSubscriptionEnabled(id, enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"id": id, "enabled": enabled})
	writeOK(w)
}

func (rt *Router) setExternalServersEnabled(w http.ResponseWriter, r *http.Request, id int64) {
	enabled, ok := enabledBody(w, r)
	if !ok {
		return
	}
	if err := rt.mgr.SetExtSubscriptionServersEnabled(id, enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"id": id, "enabled": enabled, "all": true})
	writeOK(w)
}

func (rt *Router) setExternalServerEnabled(w http.ResponseWriter, r *http.Request, id int64) {
	enabled, ok := enabledBody(w, r)
	if !ok {
		return
	}
	if err := rt.mgr.SetExtServerEnabled(id, enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"server": id, "enabled": enabled})
	writeOK(w)
}
