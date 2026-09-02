package server

import "net/http"

// resetConnections puts the master's connection surface back to its factory
// state and answers with the surface as it now is — the same shape a save
// returns, so the editor reloads from it.
func (rt *Router) resetConnections(w http.ResponseWriter, r *http.Request) {
	st, err := rt.mgr.ResetConnections(r.Context())
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"reset": true})
	writeJSON(w, http.StatusOK, st)
}

// resetNodeConnections is the same for one node.
func (rt *Router) resetNodeConnections(w http.ResponseWriter, r *http.Request, id int64) {
	st, err := rt.mgr.ResetNodeConnections(r.Context(), id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{"reset": true, "node": id})
	writeJSON(w, http.StatusOK, st)
}
