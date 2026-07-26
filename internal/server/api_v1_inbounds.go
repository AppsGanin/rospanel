package server

import "net/http"

// Custom inbounds over the external API. The shapes are the panel's (inboundReq in,
// core.InboundView out), so an integration and the panel can never drift into
// describing an inbound differently.
//
// Routes are keyed by SERVER id for list/create and by the inbound's own id for
// edit/delete — the same split as the panel, and for the same reason: an inbound
// belongs to exactly one machine, and its id already says which.

// apiListInbounds returns one server's custom inbounds (0 = the master).
func (rt *Router) apiListInbounds(w http.ResponseWriter, _ *http.Request, serverID int64) {
	list, err := rt.mgr.Inbounds(serverID)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, list)
}

// apiCreateInbound adds a custom inbound to one server.
func (rt *Router) apiCreateInbound(w http.ResponseWriter, r *http.Request, serverID int64) {
	var req inboundReq
	if !apiDecode(w, r, &req) {
		return
	}
	in, err := req.toModel(serverID, 0)
	if err != nil {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	v, err := rt.mgr.CreateInbound(r.Context(), in)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusCreated, v)
}

// apiUpdateInbound edits one custom inbound.
func (rt *Router) apiUpdateInbound(w http.ResponseWriter, r *http.Request, id int64) {
	var req inboundReq
	if !apiDecode(w, r, &req) {
		return
	}
	in, err := req.toModel(0, id)
	if err != nil {
		writeAPIErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	v, err := rt.mgr.UpdateInbound(r.Context(), in)
	if err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, v)
}

// apiDeleteInbound removes one custom inbound.
func (rt *Router) apiDeleteInbound(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteInbound(id); err != nil {
		writeAPIManagerErr(w, err)
		return
	}
	writeAPIData(w, http.StatusOK, map[string]bool{"ok": true})
}
