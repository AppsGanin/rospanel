package server

import (
	"errors"
	"net/http"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Incidents and maintenance windows for the public status page.

// incidentReq is an incident as the editor sends it. Notify sends it to the users
// through the bot; Lang is the admin's language, which the message's own words
// (not the operator's text) are written in.
type incidentReq struct {
	Kind     string  `json:"kind"`
	Title    string  `json:"title"`
	Body     string  `json:"body"`
	NodeIDs  []int64 `json:"node_ids"`
	StartsAt int64   `json:"starts_at"`
	EndsAt   int64   `json:"ends_at"`
	// HideInSub takes the named servers out of subscriptions while maintenance is
	// under way.
	HideInSub bool   `json:"hide_in_sub"`
	Notify    bool   `json:"notify"`
	Lang      string `json:"lang"`
}

func (rt *Router) listIncidents(w http.ResponseWriter, _ *http.Request) {
	incidents, err := rt.mgr.ListIncidents()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	servers, err := rt.mgr.IncidentServers()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": incidents, "servers": servers})
}

func (rt *Router) createIncident(w http.ResponseWriter, r *http.Request) {
	rt.saveIncident(w, r, 0)
}

func (rt *Router) updateIncident(w http.ResponseWriter, r *http.Request, id int64) {
	rt.saveIncident(w, r, id)
}

func (rt *Router) saveIncident(w http.ResponseWriter, r *http.Request, id int64) {
	var req incidentReq
	if !decodeJSON(w, r, &req) || !rt.mayNotify(w, r, req.Notify) {
		return
	}
	in, err := rt.mgr.SaveIncident(model.Incident{
		ID: id, Kind: req.Kind, Title: req.Title, Body: req.Body,
		NodeIDs: req.NodeIDs, StartsAt: req.StartsAt, EndsAt: req.EndsAt, HideInSub: req.HideInSub,
	})
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.dropStatusCache()
	rt.answerIncident(w, r, in, req.Notify, req.Lang, false)
}

func (rt *Router) resolveIncident(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Note   string `json:"note"` // the closing words of its course, optional
		Notify bool   `json:"notify"`
		Lang   string `json:"lang"`
	}
	if !decodeJSON(w, r, &req) || !rt.mayNotify(w, r, req.Notify) {
		return
	}
	in, changed, err := rt.mgr.ResolveIncident(id, req.Note)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.dropStatusCache()
	// Only the call that ended it announces it: a second click is not news.
	rt.answerIncident(w, r, in, req.Notify && changed, req.Lang, true)
}

// compensateIncident adds hours to the subscriptions of the users a finished
// incident hit, and tells them when asked.
func (rt *Router) compensateIncident(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Hours  int  `json:"hours"`
		Notify bool `json:"notify"`
	}
	if !decodeJSON(w, r, &req) || !rt.mayNotify(w, r, req.Notify) {
		return
	}
	in, err := rt.mgr.CompensateIncident(r.Context(), id, req.Hours, req.Notify)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incident": in})
}

// addIncidentUpdate posts a step of an incident's course, and tells the users
// about it when asked.
func (rt *Router) addIncidentUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Stage  string `json:"stage"`
		Body   string `json:"body"`
		Notify bool   `json:"notify"`
		Lang   string `json:"lang"`
	}
	if !decodeJSON(w, r, &req) || !rt.mayNotify(w, r, req.Notify) {
		return
	}
	in, u, err := rt.mgr.AddIncidentUpdate(id, req.Stage, req.Body)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.dropStatusCache()
	out := map[string]any{"incident": in}
	if req.Notify {
		if err := rt.mgr.NotifyIncidentUpdate(r.Context(), in, u, i18n.Normalize(req.Lang)); err != nil {
			out["notify_error"] = codedErr(err)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (rt *Router) deleteIncidentUpdate(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteIncidentUpdate(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.dropStatusCache()
	writeOK(w)
}

func (rt *Router) deleteIncident(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteIncident(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.dropStatusCache()
	writeOK(w)
}

// mayNotify refuses a notification from an admin who may not send broadcasts: it
// reaches every subscriber, exactly as a broadcast does.
func (rt *Router) mayNotify(w http.ResponseWriter, r *http.Request, notify bool) bool {
	if notify && !callerPerms(r).Has(model.PermBroadcasts) {
		writeErrCode(w, http.StatusForbidden, "err.forbidden", "недостаточно прав")
		return false
	}
	return true
}

// answerIncident sends the notification when asked and returns the incident. The
// incident is saved either way, so a notification that could not go out (no user
// bot, nobody to send to) is reported beside it rather than as a failed save.
func (rt *Router) answerIncident(w http.ResponseWriter, r *http.Request, in *model.Incident, notify bool, lang string, resolved bool) {
	out := map[string]any{"incident": in}
	if notify {
		if err := rt.mgr.NotifyIncident(r.Context(), in, i18n.Normalize(lang), resolved); err != nil {
			out["notify_error"] = codedErr(err)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// codedErr is a failure beside a success, as the panel renders it: the dictionary
// code and its arguments when there are some, the text otherwise.
func codedErr(err error) map[string]any {
	out := map[string]any{"error": err.Error()}
	var ve *core.ValidationError
	if errors.As(err, &ve) {
		out["code"], out["args"] = ve.Code, ve.Args
	}
	return out
}
