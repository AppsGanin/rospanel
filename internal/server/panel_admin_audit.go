package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The admin trail surface — owner-only (see panelMux). It pages backwards with
// ?before=<id>, the id of the oldest row the client already has, exactly like the
// user journal.

type adminAuditResponse struct {
	Events     []model.AdminAudit `json:"events"`
	NextBefore int64              `json:"next_before"`
}

// adminAudit returns the admin trail, optionally filtered by category (the journal's
// dropdown: "Настройки", "Администраторы", …), by a single action, or by actor.
func (rt *Router) adminAudit(w http.ResponseWriter, r *http.Request) {
	limit, before := eventPageArgs(r)
	q := r.URL.Query()

	// A category expands to the actions it holds; a bare action is still accepted so
	// a row's own key can be used as a filter.
	var actions []string
	if cat := strings.TrimSpace(q.Get("category")); cat != "" {
		actions = model.AdminAuditActionsIn(cat)
		if len(actions) == 0 {
			// An unknown category matches nothing rather than everything — a filter
			// that quietly ignores itself and dumps the whole trail is a lie.
			writeJSON(w, http.StatusOK, adminAuditResponse{Events: []model.AdminAudit{}})
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
		writeManagerErr(w, err)
		return
	}
	if events == nil {
		events = []model.AdminAudit{}
	}
	// A cursor only when the page came back full — a short page means there is
	// nothing older to fetch.
	var next int64
	if len(events) == limit && limit > 0 {
		next = events[len(events)-1].ID
	}
	writeJSON(w, http.StatusOK, adminAuditResponse{Events: events, NextBefore: next})
}

// adminAuditCatalog returns what the journal needs to render itself: the categories
// its filter offers, and the action→label map its rows are titled from.
func (rt *Router) adminAuditCatalog(w http.ResponseWriter, _ *http.Request) {
	cats := make([]map[string]string, 0, len(model.AdminAuditCategories))
	for _, c := range model.AdminAuditCategories {
		cats = append(cats, map[string]string{"key": c.Key, "label": c.Label})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"categories": cats,
		"actions":    model.AdminAuditCatalog,
	})
}
