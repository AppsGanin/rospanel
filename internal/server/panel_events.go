package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The audit log surface: one user's trail (the «Журнал» modal in the user detail)
// and the global trail (the journal page). Both page backwards with ?before=<id>,
// the id of the oldest row the client already has — the rows are id-ordered, so it
// stays a stable cursor even as new events land at the top.

// eventsResponse is the paged envelope both endpoints return. Events is never null
// (the UI iterates it directly); next_before is 0 when the last page was reached.
type eventsResponse struct {
	Events     []model.UserEvent `json:"events"`
	NextBefore int64             `json:"next_before"`
}

// makeEventsResponse builds the envelope, reporting a cursor only when the page came
// back full — a short page means there is nothing older to fetch.
func makeEventsResponse(events []model.UserEvent, limit int) eventsResponse {
	if events == nil {
		events = []model.UserEvent{}
	}
	var next int64
	if len(events) == limit && limit > 0 {
		next = events[len(events)-1].ID
	}
	return eventsResponse{Events: events, NextBefore: next}
}

// eventPageArgs reads the shared ?limit / ?before paging params. The limit is
// clamped here, not just in the manager, so the caller's full-page cursor check
// compares against the size the store was really asked for.
func eventPageArgs(r *http.Request) (limit int, before int64) {
	q := r.URL.Query()
	limit = core.EventPageLimit(atoiOr(q.Get("limit"), 0))
	before, _ = strconv.ParseInt(strings.TrimSpace(q.Get("before")), 10, 64)
	return limit, before
}

// userEvents returns one user's audit trail (the «Журнал» modal).
func (rt *Router) userEvents(w http.ResponseWriter, r *http.Request, id int64) {
	limit, before := eventPageArgs(r)
	events, err := rt.mgr.UserEvents(id, limit, before)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, makeEventsResponse(events, limit))
}

// events returns the global audit trail, optionally filtered by action, actor kind
// or user (the journal page).
func (rt *Router) events(w http.ResponseWriter, r *http.Request) {
	limit, before := eventPageArgs(r)
	q := r.URL.Query()
	userID, _ := strconv.ParseInt(strings.TrimSpace(q.Get("user_id")), 10, 64)
	// An unknown action would otherwise return an empty page — indistinguishable from
	// "nothing happened" — so a typo'd filter fails loudly instead.
	action := strings.TrimSpace(q.Get("action"))
	if action != "" && !model.ValidUserEvent(action) {
		writeErr(w, http.StatusBadRequest, "неизвестное событие")
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
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, makeEventsResponse(events, limit))
}

// eventCatalog returns the stable action key→label list so the UI renders event
// names (and builds its filter dropdown) from one source of truth.
func (rt *Router) eventCatalog(w http.ResponseWriter, _ *http.Request) {
	out := make([]map[string]string, 0, len(model.UserEventCatalog))
	for _, e := range model.UserEventCatalog {
		out = append(out, map[string]string{"key": e.Key, "label": e.Label})
	}
	writeJSON(w, http.StatusOK, out)
}
