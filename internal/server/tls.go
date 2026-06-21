package server

import (
	"net/http"
	"strings"
)

func (rt *Router) tlsStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := rt.mgr.TLSStatus()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// setACME points TLS at a domain or IP, optionally switches the ACME CA to
// ZeroSSL (provider="zerossl", plus eab_key_id / eab_hmac_key), and obtains a
// certificate (blocking on the HTTP-01 challenge — port 80 must be reachable).
func (rt *Router) setACME(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target     string `json:"target"`
		Email      string `json:"email"`
		Provider   string `json:"provider"`     // "letsencrypt" | "zerossl"
		EABKeyID   string `json:"eab_key_id"`   // ZeroSSL EAB Key ID
		EABHMACKey string `json:"eab_hmac_key"` // ZeroSSL EAB HMAC key (base64url)
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Target) == "" {
		writeErr(w, http.StatusBadRequest, "укажите домен или IP-адрес")
		return
	}
	if err := rt.mgr.SetACMETarget(req.Target, req.Email, req.Provider, req.EABKeyID, req.EABHMACKey); err != nil {
		writeErr(w, http.StatusBadRequest, "не удалось получить сертификат: "+err.Error())
		return
	}
	rt.tlsStatus(w, r)
}
