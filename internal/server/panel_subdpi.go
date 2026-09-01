package server

import (
	"net/http"

	"github.com/AppsGanin/rospanel/internal/model"
)

// saveSubDPI stores the client-side DPI evasion block (Settings → Subscriptions).
// Validated as a unit: a range Xray would refuse must not reach every client as a
// config that fails to load.
func (rt *Router) saveSubDPI(w http.ResponseWriter, r *http.Request) {
	var req model.SubDPI
	if !decodeJSON(w, r, &req) {
		return
	}
	req = req.Normalized()
	if err := req.Validate(); err != nil {
		writeManagerErr(w, err)
		return
	}
	if err := rt.mgr.Store().SetSubDPI(req); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{
		"json_clients": req.JSONClients, "fragment": req.Fragment, "noise": req.Noise,
		"record_fragment": req.RecordFragment,
	})
	writeOK(w)
}
