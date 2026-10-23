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
		// Empty fields are the normal case: the panel fills its own defaults. Set them
		// only when the other side expects particular values.
		Identity model.ExtIdentity `json:"identity"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sub, report, err := rt.mgr.CreateExtSubscription(r.Context(), req.Name, req.Source, req.Identity)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"id": sub.ID, "name": sub.Name, "servers": report.Total})
	writeJSON(w, http.StatusOK, map[string]any{"subscription": sub, "report": report})
}

// updateExternalSource changes where a subscription is read from and the device
// identity it presents, then re-reads it. Both in one request because the editor shows
// them together, and because changing the source usually means a different upstream,
// where the old device id means nothing.
func (rt *Router) updateExternalSource(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Name     string            `json:"name"`
		Source   string            `json:"source"`
		Identity model.ExtIdentity `json:"identity"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	report, err := rt.mgr.UpdateExtSubscriptionSource(r.Context(), id, req.Name, req.Source, req.Identity)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"id": id, "servers": report.Total})
	writeJSON(w, http.StatusOK, map[string]any{"report": report})
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

// setExternalRelay sets the lanes of our servers a subscription is relayed through
// ({relays: [{server_id, lane}]}); none hands its servers out as they are. The older
// single {lane, server_id} is still taken.
func (rt *Router) setExternalRelay(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Relays   *[]model.ExtRelayVia `json:"relays"`
		Routing  *model.ExtRouting    `json:"routing"` // saved with the relays when given
		Lane     string               `json:"lane"`
		ServerID int64                `json:"server_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	relays := []model.ExtRelayVia{}
	switch {
	case req.Relays != nil:
		relays = *req.Relays
	case req.Lane != "":
		relays = []model.ExtRelayVia{{ServerID: req.ServerID, Lane: req.Lane}}
	}
	sub, err := rt.mgr.SetExtSubscriptionRelays(id, relays, req.Routing)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"id": id, "relays": sub.Relays})
	writeJSON(w, http.StatusOK, map[string]any{"subscription": sub})
}

// setExternalRouting sets how a relayed subscription's traffic leaves the relay server
// and answers the subscription as stored.
func (rt *Router) setExternalRouting(w http.ResponseWriter, r *http.Request, id int64) {
	var req model.ExtRouting
	if !decodeJSON(w, r, &req) {
		return
	}
	sub, err := rt.mgr.SetExtSubscriptionRouting(id, req)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"id": id, "rules": len(sub.Routing.Rules), "rest": sub.Routing.RestTarget(), "dial": sub.Routing.Dial})
	writeJSON(w, http.StatusOK, map[string]any{"subscription": sub})
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

// setExternalServerName sets the name users see for an external server ({name}; empty
// puts the source's back).
func (rt *Router) setExternalServerName(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SetExtServerName(id, req.Name); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"server": id, "name": req.Name})
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
