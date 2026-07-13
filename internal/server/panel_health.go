package server

import (
	"context"
	"net/http"
	"time"
)

// health returns the panel self-diagnostics for the Health page (Xray, config,
// TLS, disk/RAM, geo databases, egress lanes).
func (rt *Router) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, rt.mgr.Health())
}

// selfTestBudget caps the whole run: it spawns a throwaway Xray per protocol and
// dials the internet through each. Comfortably covers four cold handshakes while
// still bounding a stuck request.
const selfTestBudget = 90 * time.Second

// selfTest connects to each enabled protocol as a real client and reports whether
// traffic flows end-to-end. It's a POST because it does real work (spawns a client,
// sends traffic), not because it changes state — it changes nothing.
func (rt *Router) selfTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), selfTestBudget)
	defer cancel()

	results, err := rt.mgr.SelfTest(ctx)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
