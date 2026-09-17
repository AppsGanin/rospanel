package server

import (
	"net/http"
	"net/netip"
	"strings"
)

// Addresses dropped at the firewall: banned by hand from a user's addresses, and every
// ban the panel holds, whatever placed it. Owner and admin only, like the source policy
// — a ban cuts everyone behind an address off every server.

// listBans returns every ban, newest first.
func (rt *Router) listBans(w http.ResponseWriter, _ *http.Request) {
	bans, err := rt.mgr.Bans()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bans": bans,
		// Whether this machine can drop anything at all: without nftables a ban is
		// recorded and handed to the nodes, and the panel says the master is not
		// enforcing it.
		"can_enforce": rt.mgr.CanBlockIPs(),
	})
}

// banIP bans an address until it is lifted. The caller's own address is refused, so an
// operator cannot lock themselves out of the panel with one click.
func (rt *Router) banIP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IP     string `json:"ip"`
		UserID int64  `json:"user_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ip, err := rt.mgr.BanIP(req.IP, req.UserID, clientIP(r))
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	auditTarget(r, ip)
	if req.UserID > 0 {
		auditDetails(r, map[string]any{"user_id": req.UserID})
	}
	writeOK(w)
}

// unbanIP lifts every ban on an address.
func (rt *Router) unbanIP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IP string `json:"ip"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ip := strings.TrimSpace(req.IP)
	// The journal names the address as it is written everywhere else.
	if a, err := netip.ParseAddr(ip); err == nil && a.Zone() == "" {
		ip = a.Unmap().String()
	}
	gone, err := rt.mgr.UnbanIP(ip)
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
