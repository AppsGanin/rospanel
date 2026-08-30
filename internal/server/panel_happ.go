package server

import (
	"encoding/json"
	"net/http"

	"github.com/Shu1t3/rospanel-shu1t3/internal/happ"
)

// ── POST /api/happ/subscriptions ──────────────────────────────────────────
// Create a subscription and run first sync.

func (rt *Router) createHappSubscription(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.URL == "" {
		writeErr(w, http.StatusBadRequest, "url required")
		return
	}
	id, res, err := rt.mgr.CreateHappSubscription(r.Context(), body.Name, body.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	nodeCount := 0
	if res != nil {
		nodeCount = res.Total
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         id,
		"node_count": nodeCount,
	})
}

// ── GET /api/happ/subscriptions ───────────────────────────────────────────
// List all subscriptions (for internal management).

func (rt *Router) listHappSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := rt.mgr.ListHappSubscriptions()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if subs == nil {
		subs = []*happ.Subscription{}
	}
	writeJSON(w, http.StatusOK, subs)
}

// ── DELETE /api/happ/subscriptions/{id} ─────────────────────────────────

func (rt *Router) deleteHappSubscription(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.DeleteHappSubscription(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── POST /api/happ/subscriptions/{id}/sync ──────────────────────────────

func (rt *Router) syncHappSubscription(w http.ResponseWriter, r *http.Request, id int64) {
	res, err := rt.mgr.SyncHappSubscription(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"added":   res.Added,
		"updated": res.Updated,
		"total":   res.Total,
	})
}

// ── GET /api/happ/nodes ──────────────────────────────────────────────────
// List all Happ nodes for UI display.

func (rt *Router) listHappNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := rt.mgr.ListAllHappNodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if nodes == nil {
		nodes = []*happ.Node{}
	}
	writeJSON(w, http.StatusOK, nodes)
}

// ── POST /api/happ/nodes/{id}/enabled ───────────────────────────────────

func (rt *Router) setHappNodeEnabled(w http.ResponseWriter, r *http.Request, id int64) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := rt.mgr.SetHappNodeEnabled(id, body.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── DELETE /api/happ/nodes/{id} ──────────────────────────────────────────

func (rt *Router) deleteHappNode(w http.ResponseWriter, r *http.Request, id int64) {
	if err := rt.mgr.DeleteHappNode(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
