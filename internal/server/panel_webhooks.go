package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// validateWebhook checks the URL (http/https with a host) and the subscribed
// event keys. It writes the error response and returns false on failure.
func validateWebhook(w http.ResponseWriter, url string, events []string) bool {
	if err := model.ValidWebhookURL(url); err != nil {
		writeErr(w, http.StatusBadRequest, "неверный URL вебхука: "+err.Error())
		return false
	}
	for _, e := range events {
		if !model.ValidWebhookEvent(e) {
			writeErr(w, http.StatusBadRequest, "неизвестное событие: "+e)
			return false
		}
	}
	return true
}

// listWebhooks returns the configured endpoints plus the event catalog the UI
// renders as checkboxes.
func (rt *Router) listWebhooks(w http.ResponseWriter, _ *http.Request) {
	hooks, err := rt.mgr.Store().ListWebhooks()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if hooks == nil {
		hooks = []model.Webhook{}
	}
	catalog := make([]map[string]string, 0, len(model.WebhookEventCatalog))
	for _, e := range model.WebhookEventCatalog {
		catalog = append(catalog, map[string]string{"key": e.Key, "label": e.Label})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"webhooks": hooks,
		"events":   catalog,
	})
}

func (rt *Router) createWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if !validateWebhook(w, req.URL, req.Events) {
		return
	}
	h, err := rt.mgr.Store().CreateWebhook(req.URL, req.Events)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h)
}

func (rt *Router) updateWebhook(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		URL     string   `json:"url"`
		Events  []string `json:"events"`
		Enabled bool     `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if !validateWebhook(w, req.URL, req.Events) {
		return
	}
	if err := rt.mgr.Store().UpdateWebhook(id, req.URL, req.Events, req.Enabled); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

func (rt *Router) deleteWebhook(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.Store().DeleteWebhook(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// testWebhook sends a synchronous "ping" delivery and reports the HTTP status so
// the operator can confirm the endpoint is reachable and verifying the signature.
func (rt *Router) testWebhook(w http.ResponseWriter, _ *http.Request, id int64) {
	status, err := rt.mgr.TestWebhook(id)
	resp := map[string]any{"status": status, "ok": err == nil}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}
