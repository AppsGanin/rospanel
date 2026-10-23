package server

import (
	"errors"
	"net/http"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Incidents and maintenance windows over /v1: what the panel's editor does, for a
// monitoring system or the operator's own bot.

type (
	apiIncidentsResp struct {
		Incidents []model.Incident `json:"incidents"`
		// Servers are the ones an incident can name in node_ids (0 is the panel's own).
		Servers []core.IncidentServer `json:"servers"`
	}
	// apiIncidentReq is an incident to create or rewrite. notify also sends it to
	// every user with an active subscription through the user bot (needs the
	// broadcasts permission); lang is the language of the message's own words.
	apiIncidentReq struct {
		Kind     string  `json:"kind"`
		Title    string  `json:"title"`
		Body     string  `json:"body"`
		NodeIDs  []int64 `json:"node_ids"`
		StartsAt int64   `json:"starts_at"`
		EndsAt   int64   `json:"ends_at"`
		// HideInSub takes the named servers out of subscriptions while maintenance
		// is under way.
		HideInSub bool   `json:"hide_in_sub"`
		Notify    bool   `json:"notify"`
		Lang      string `json:"lang"`
	}
	apiIncidentResolveReq struct {
		Note   string `json:"note"` // closes its course as a "resolved" update, optional
		Notify bool   `json:"notify"`
		Lang   string `json:"lang"`
	}
	// apiIncidentCompensateReq gives the users a finished incident hit hours back:
	// hours from 1 to 2160; notify tells each of them through the user bot (needs the
	// broadcasts permission).
	apiIncidentCompensateReq struct {
		Hours  int  `json:"hours"`
		Notify bool `json:"notify"`
	}
	// apiIncidentUpdateReq is a step of an incident's course: stage investigating |
	// identified | monitoring (incidents only) | update (default).
	apiIncidentUpdateReq struct {
		Stage  string `json:"stage"`
		Body   string `json:"body"`
		Notify bool   `json:"notify"`
		Lang   string `json:"lang"`
	}
	// apiIncidentResp is the stored incident; notify_error says why the message to
	// the users did not go out (the incident is saved regardless).
	apiIncidentResp struct {
		Incident    *model.Incident `json:"incident"`
		NotifyError *apiNotifyError `json:"notify_error,omitempty"`
	}
	apiNotifyError struct {
		Key     string         `json:"key,omitempty"`
		Message string         `json:"message"`
		Args    map[string]any `json:"args,omitempty"`
	}
)

func (rt *Router) apiListIncidents(w http.ResponseWriter, _ *http.Request) {
	incidents, err := rt.mgr.ListIncidents()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	servers, err := rt.mgr.IncidentServers()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiIncidentsResp{Incidents: incidents, Servers: servers})
}

func (rt *Router) apiCreateIncident(w http.ResponseWriter, r *http.Request) {
	rt.apiSaveIncident(w, r, 0)
}

func (rt *Router) apiUpdateIncident(w http.ResponseWriter, r *http.Request, id int64) {
	rt.apiSaveIncident(w, r, id)
}

func (rt *Router) apiSaveIncident(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiIncidentReq
	if !apiDecode(w, r, &req) || !apiMayNotify(w, r, req.Notify) {
		return
	}
	in, err := rt.mgr.SaveIncident(model.Incident{
		ID: id, Kind: req.Kind, Title: req.Title, Body: req.Body,
		NodeIDs: req.NodeIDs, StartsAt: req.StartsAt, EndsAt: req.EndsAt, HideInSub: req.HideInSub,
	})
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.dropStatusCache()
	status := http.StatusOK
	if id == 0 {
		status = http.StatusCreated
	}
	writeAPIData(w, status, rt.apiNotifyIncident(r, in, req.Notify, req.Lang, false))
}

func (rt *Router) apiResolveIncident(w http.ResponseWriter, r *http.Request, id int64) {
	// The body is optional: a bare POST resolves without telling anyone.
	var req apiIncidentResolveReq
	if r.ContentLength != 0 && !apiDecode(w, r, &req) {
		return
	}
	if !apiMayNotify(w, r, req.Notify) {
		return
	}
	in, changed, err := rt.mgr.ResolveIncident(id, req.Note)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.dropStatusCache()
	// Only the call that ended it announces it: a repeat is not news.
	writeAPIData(w, http.StatusOK, rt.apiNotifyIncident(r, in, req.Notify && changed, req.Lang, true))
}

// apiCompensateIncident adds hours to the subscriptions of the users the incident hit.
func (rt *Router) apiCompensateIncident(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiIncidentCompensateReq
	if !apiDecode(w, r, &req) || !apiMayNotify(w, r, req.Notify) {
		return
	}
	in, err := rt.mgr.CompensateIncident(r.Context(), id, req.Hours, req.Notify)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiIncidentResp{Incident: in})
}

func (rt *Router) apiAddIncidentUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiIncidentUpdateReq
	if !apiDecode(w, r, &req) || !apiMayNotify(w, r, req.Notify) {
		return
	}
	in, u, err := rt.mgr.AddIncidentUpdate(id, req.Stage, req.Body)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.dropStatusCache()
	out := apiIncidentResp{Incident: in}
	if req.Notify {
		out.NotifyError = apiNotifyErr(rt.mgr.NotifyIncidentUpdate(r.Context(), in, u, i18n.Normalize(req.Lang)))
	}
	writeAPIData(w, http.StatusCreated, out)
}

func (rt *Router) apiDeleteIncidentUpdate(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteIncidentUpdate(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.dropStatusCache()
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *Router) apiDeleteIncident(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteIncident(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	rt.dropStatusCache()
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiMayNotify refuses a notification from a key without the broadcasts
// permission, as the panel does for an admin.
func apiMayNotify(w http.ResponseWriter, r *http.Request, notify bool) bool {
	if notify && !apiPerms(r).Has(model.PermBroadcasts) {
		writeAPIErr(w, http.StatusForbidden, "forbidden", "notify needs the broadcasts permission")
		return false
	}
	return true
}

// apiNotifyIncident sends the notification when asked; a failure is reported in
// English beside the saved incident.
func (rt *Router) apiNotifyIncident(r *http.Request, in *model.Incident, notify bool, lang string, resolved bool) apiIncidentResp {
	out := apiIncidentResp{Incident: in}
	if !notify {
		return out
	}
	out.NotifyError = apiNotifyErr(rt.mgr.NotifyIncident(r.Context(), in, i18n.Normalize(lang), resolved))
	return out
}

// apiNotifyErr is a notification failure in the API's terms: the reason's key and
// its English text, nil when there was none.
func apiNotifyErr(err error) *apiNotifyError {
	if err == nil {
		return nil
	}
	ne := &apiNotifyError{Message: err.Error()}
	var ve *core.ValidationError
	if errors.As(err, &ve) {
		ne.Key, ne.Args = ve.Code, ve.Args
		if msg, ok := i18n.ErrorEN(ve.Code, ve.Args); ok {
			ne.Message = msg
		}
	}
	return ne
}
