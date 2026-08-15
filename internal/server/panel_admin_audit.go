package server

import (
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// adminAuditFilterFromQuery builds the store filter shared by the paged list and the
// CSV export, so a search/date/actor/category filter can never mean one thing on
// screen and another in the exported file. ok=false means the query named a category
// that expands to no actions — the caller must return an empty result, not the whole
// trail (a filter that silently ignores itself is a lie).
func adminAuditFilterFromQuery(r *http.Request, limit int, before int64) (store.AdminAuditFilter, bool) {
	q := r.URL.Query()

	// A category expands to the actions it holds; a bare action is still accepted so
	// a row's own key can be used as a filter.
	var actions []string
	if cat := strings.TrimSpace(q.Get("category")); cat != "" {
		actions = model.AdminAuditActionsIn(cat)
		if len(actions) == 0 {
			return store.AdminAuditFilter{}, false
		}
	} else if a := strings.TrimSpace(q.Get("action")); a != "" {
		actions = []string{a}
	}

	from, _ := strconv.ParseInt(strings.TrimSpace(q.Get("from")), 10, 64)
	to, _ := strconv.ParseInt(strings.TrimSpace(q.Get("to")), 10, 64)
	return store.AdminAuditFilter{
		Actions:  actions,
		Actor:    strings.TrimSpace(q.Get("actor")),
		Search:   strings.TrimSpace(q.Get("search")),
		Since:    from,
		Until:    to,
		BeforeID: before,
		Limit:    limit,
	}, true
}

// adminAudit returns the admin trail, optionally filtered by category (the journal's
// dropdown: "Settings", "Administrators", …), a single action, an actor, a free-text
// search, or a created-at range (?from / ?to, unix seconds).
func (rt *Router) adminAudit(w http.ResponseWriter, r *http.Request) {
	limit, before := eventPageArgs(r)
	f, ok := adminAuditFilterFromQuery(r, limit, before)
	if !ok {
		writeJSON(w, http.StatusOK, adminAuditResponse{Events: []model.AdminAudit{}})
		return
	}

	events, err := rt.mgr.AdminAudit(f)
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

// exportAdminAudit streams the filtered trail as CSV (newest first). It honours the
// same filters as adminAudit except the page cursor — an export is the whole matching
// set, capped in the manager. Owner-only cookie auth, so a plain <a download> works.
func (rt *Router) exportAdminAudit(w http.ResponseWriter, r *http.Request) {
	f, ok := adminAuditFilterFromQuery(r, 0, 0)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="rospanel-audit.csv"`)
	w.Header().Set("Cache-Control", "no-store")

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"time", "action", "target", "actor_kind", "actor", "ip", "details"})
	if ok {
		err := rt.mgr.ExportAdminAudit(f, func(ev model.AdminAudit) error {
			det := ""
			if ev.Details != nil {
				if b, e := json.Marshal(ev.Details); e == nil {
					det = string(b)
				}
			}
			ts := time.Unix(ev.CreatedAt, 0).UTC().Format(time.RFC3339)
			return cw.Write([]string{ts, ev.Action, ev.Target, ev.ActorKind, ev.ActorName, ev.IP, det})
		})
		if err != nil {
			// The 200 and header row are already on the wire, so there is nothing to
			// do but stop and note it; the client gets a truncated file.
			slog.Warn("admin audit: CSV export failed mid-stream", "err", err)
		}
	}
	cw.Flush()
}

// adminAuditCatalog returns what the journal needs to render itself: the categories
// its filter offers and the actions each one expands to. Labels come from the
// panel's own dictionaries, so only keys travel.
func (rt *Router) adminAuditCatalog(w http.ResponseWriter, _ *http.Request) {
	cats := make([]map[string]string, 0, len(model.AdminAuditCategories))
	for _, c := range model.AdminAuditCategories {
		cats = append(cats, map[string]string{"key": c})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"categories": cats,
		"actions":    model.AdminAuditCatalog,
	})
}
