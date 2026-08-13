package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Webhooks over the external API. They were panel-only, which made the push half of
// an integration the one thing you could not set up from the integration: a key could
// create users and read stats, but the endpoint those events are delivered to had to
// be typed into the panel by hand.

type (
	// apiWebhookReq is the editable shape of a webhook. Enabled is a pointer so a
	// create (where it is absent) means "on" while an update can switch it off.
	apiWebhookReq struct {
		URL     string   `json:"url"`
		Events  []string `json:"events"`
		Enabled *bool    `json:"enabled,omitempty"`
	}
	// apiWebhookTestResp is what a test delivery reports: the endpoint's HTTP status
	// and whether it counted as a success.
	apiWebhookTestResp struct {
		Status int    `json:"status"`
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
	}
	// apiEventKey is one subscribable key. An object rather than a bare string so the
	// list can grow a label or a description without breaking clients.
	apiEventKey struct {
		Key string `json:"key"`
	}
)

// apiValidateWebhook rejects a malformed URL or an unknown event key, writing the
// error itself. The same two checks the panel makes — a webhook that names an event
// nobody emits would silently never fire.
func apiValidateWebhook(w http.ResponseWriter, url string, events []string) bool {
	if err := model.ValidWebhookURL(url); err != nil {
		writeAPIManagerErr(w, err)
		return false
	}
	for _, e := range events {
		if !model.ValidWebhookEvent(e) {
			writeAPIErr(w, http.StatusBadRequest, "bad_request", "unknown event: "+e)
			return false
		}
	}
	return true
}

func (rt *Router) apiListWebhooks(w http.ResponseWriter, _ *http.Request) {
	hooks, err := rt.mgr.Store().ListWebhooks()
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	if hooks == nil {
		hooks = []model.Webhook{}
	}
	writeAPIData(w, http.StatusOK, hooks)
}

// apiWebhookEvents lists the keys a webhook can subscribe to. Its own endpoint rather
// than a field on the list: the catalog is static, and a client that only wants to
// render checkboxes should not have to read every configured endpoint to get it.
func (rt *Router) apiWebhookEvents(w http.ResponseWriter, _ *http.Request) {
	out := make([]apiEventKey, 0, len(model.WebhookEventCatalog))
	for _, e := range model.WebhookEventCatalog {
		out = append(out, apiEventKey{Key: e})
	}
	writeAPIData(w, http.StatusOK, out)
}

func (rt *Router) apiCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var req apiWebhookReq
	if !apiDecode(w, r, &req) {
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if !apiValidateWebhook(w, req.URL, req.Events) {
		return
	}
	// Absent means on: the field is optional, and an endpoint you bothered to
	// register is one you want delivering.
	enabled := req.Enabled == nil || *req.Enabled
	h, err := rt.mgr.Store().CreateWebhook(req.URL, req.Events, enabled)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusCreated, h)
}

func (rt *Router) apiUpdateWebhook(w http.ResponseWriter, r *http.Request, id int64) {
	var req apiWebhookReq
	if !apiDecode(w, r, &req) {
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if !apiValidateWebhook(w, req.URL, req.Events) {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if err := rt.mgr.Store().UpdateWebhook(id, req.URL, req.Events, enabled); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rt *Router) apiDeleteWebhook(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.Store().DeleteWebhook(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiTestWebhook sends a synchronous "ping" delivery. A failing endpoint is NOT an
// API error — the call did what it was asked and the answer is the failure, so it
// comes back 200 with ok=false and the transport error, the way the panel shows it.
func (rt *Router) apiTestWebhook(w http.ResponseWriter, _ *http.Request, id int64) {
	status, err := rt.mgr.TestWebhook(id)
	resp := apiWebhookTestResp{Status: status, OK: err == nil}
	if err != nil {
		resp.Error = err.Error()
	}
	writeAPIData(w, http.StatusOK, resp)
}
