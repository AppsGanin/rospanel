package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The source policy: where clients may connect from, and who it has refused. Owner
// and admin only — it can cut off paying users, and the block list names the
// addresses it cut.

func (rt *Router) getConnPolicy(w http.ResponseWriter, _ *http.Request) {
	blocked, err := rt.mgr.BlockedIPs(200)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"policy":  rt.mgr.ConnPolicy(),
		"blocked": blocked,
		// Whether this machine can actually drop anything. Without nftables the
		// policy still records every refusal, and the panel says so rather than
		// letting an operator believe a rule is being enforced.
		"can_enforce": rt.mgr.CanBlockIPs(),
	})
}

func (rt *Router) saveConnPolicy(w http.ResponseWriter, r *http.Request) {
	var req model.ConnPolicy
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.SaveConnPolicy(req); err != nil {
		writeManagerErr(w, err)
		return
	}
	auditDetails(r, map[string]any{
		"mode": req.Mode, "countries": len(req.Countries), "asns": len(req.ASNs), "enforce": req.Enforce,
	})
	writeOK(w)
}

// unblockIP lifts one block by hand — the operator overruling the policy for an
// address it refused.
func (rt *Router) unblockIP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IP string `json:"ip"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ip := strings.TrimSpace(req.IP)
	gone, err := rt.mgr.UnblockIP(ip)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	if !gone {
		writeErrCode(w, http.StatusNotFound, "err.blockNotFound", "этот адрес не заблокирован")
		return
	}
	auditTarget(r, ip)
	writeOK(w)
}
