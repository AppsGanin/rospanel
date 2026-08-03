package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The journals over the external API: what happened to users, and what admins (and
// API keys) did to the panel. Both were panel-only, so an integration could change
// everything and observe nothing — no feed to mirror into a CRM, a SIEM or a
// "history" tab of somebody else's UI.
//
// Both page backwards with ?before=<id>, the id of the oldest row already held. Ids
// are monotonic, so the cursor stays stable while new rows land at the top.

type (
	// apiEventsResp is the paged user-event envelope. Events is never null; NextBefore
	// is 0 once the last page was reached.
	apiEventsResp struct {
		Events     []model.UserEvent `json:"events"`
		NextBefore int64             `json:"next_before"`
	}
	// apiAdminAuditResp is the same envelope for the admin trail.
	apiAdminAuditResp struct {
		Events     []model.AdminAudit `json:"events"`
		NextBefore int64              `json:"next_before"`
	}
)

func (rt *Router) apiUserEvents(w http.ResponseWriter, r *http.Request, id int64) {
	limit, before := eventPageArgs(r)
	events, err := rt.mgr.UserEvents(id, limit, before)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiEventsResp(makeEventsResponse(events, limit)))
}

// apiEvents is the fleet-wide user journal, filterable by action, actor kind and
// user. An unknown action is rejected rather than answered with an empty page: a
// filter that silently matches nothing is indistinguishable from a quiet period.
func (rt *Router) apiEvents(w http.ResponseWriter, r *http.Request) {
	limit, before := eventPageArgs(r)
	q := r.URL.Query()
	userID, _ := strconv.ParseInt(strings.TrimSpace(q.Get("user_id")), 10, 64)
	action := strings.TrimSpace(q.Get("action"))
	if action != "" && !model.ValidUserEvent(action) {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", "unknown event: "+action)
		return
	}
	events, err := rt.mgr.Events(store.UserEventFilter{
		Action:    action,
		ActorKind: strings.TrimSpace(q.Get("actor")),
		UserID:    userID,
		BeforeID:  before,
		Limit:     limit,
	})
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, apiEventsResp(makeEventsResponse(events, limit)))
}

// apiEventCatalog lists the user-event keys, so a client can build its own filter
// (and know which keys it may see) without hard-coding the panel's list.
func (rt *Router) apiEventCatalog(w http.ResponseWriter, _ *http.Request) {
	out := make([]apiEventKey, 0, len(model.UserEventCatalog))
	for _, e := range model.UserEventCatalog {
		out = append(out, apiEventKey{Key: e})
	}
	writeAPIData(w, http.StatusOK, out)
}

// apiAdminAudit is the admin trail — including everything done through this very API,
// which is audited under the same actions the panel uses.
func (rt *Router) apiAdminAudit(w http.ResponseWriter, r *http.Request) {
	limit, before := eventPageArgs(r)
	q := r.URL.Query()

	// A category expands to the actions it holds; a bare action is accepted too, so a
	// row's own key can be pasted back as a filter.
	var actions []string
	if cat := strings.TrimSpace(q.Get("category")); cat != "" {
		actions = model.AdminAuditActionsIn(cat)
		if len(actions) == 0 {
			writeAPIErr(w, http.StatusBadRequest, "bad_request", "unknown category: "+cat)
			return
		}
	} else if a := strings.TrimSpace(q.Get("action")); a != "" {
		actions = []string{a}
	}

	events, err := rt.mgr.AdminAudit(store.AdminAuditFilter{
		Actions:  actions,
		Actor:    strings.TrimSpace(q.Get("actor")),
		BeforeID: before,
		Limit:    limit,
	})
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if events == nil {
		events = []model.AdminAudit{}
	}
	var next int64
	if len(events) == limit && limit > 0 {
		next = events[len(events)-1].ID
	}
	writeAPIData(w, http.StatusOK, apiAdminAuditResp{Events: events, NextBefore: next})
}

// apiAuditCatalogResp is the admin-audit vocabulary: the category keys a `category`
// filter accepts, and every action with the category it belongs to.
type apiAuditCatalogResp struct {
	Categories []string                `json:"categories"`
	Actions    []model.AdminAuditEntry `json:"actions"`
}

// apiAdminAuditCatalog publishes that vocabulary, which is what makes the category
// and action filters usable from outside the panel.
func (rt *Router) apiAdminAuditCatalog(w http.ResponseWriter, _ *http.Request) {
	writeAPIData(w, http.StatusOK, apiAuditCatalogResp{
		Categories: model.AdminAuditCategories,
		Actions:    model.AdminAuditCatalog,
	})
}
