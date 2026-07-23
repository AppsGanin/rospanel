package server

import "net/http"

// health returns the panel self-diagnostics for the Health page (Xray, config,
// TLS, disk/RAM, geo databases, egress lanes).
func (rt *Router) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, rt.mgr.Health())
}
