package server

import (
	"io"
	"log"
	"net/http"

	"github.com/AppsGanin/rospanel/internal/payments"
)

// handlePaymentWebhook is the public provider callback, mounted at
// /<webhook secret>/<provider key>. It always answers 200 so providers don't
// retry-storm on our internal errors — the polling loop is the safety net for
// anything missed. leaf is the provider's registry key; an unknown one is served
// the decoy, exactly like any other unrecognised path.
func handlePaymentWebhook(rt *Router, w http.ResponseWriter, r *http.Request, leaf string) {
	if r.Method != http.MethodPost {
		rt.decoy.ServeHTTP(w, r)
		return
	}
	if _, known := payments.Get(leaf); !known {
		rt.decoy.ServeHTTP(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// The raw body is handed over untouched: several providers sign the exact bytes,
	// so re-marshalling the JSON here would break their signature check.
	if err := rt.mgr.HandleProviderWebhook(leaf, body, r.Header); err != nil {
		log.Printf("[WARN] payment webhook (%s): %v", leaf, err)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
