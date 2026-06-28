package server

import (
	"io"
	"log"
	"net/http"
)

// handlePaymentWebhook is the public provider callback. It always answers 200 so
// providers don't retry-storm on our internal errors — the polling loop is the
// safety net for anything missed. leaf is "yookassa" | "cryptobot".
func handlePaymentWebhook(rt *Router, w http.ResponseWriter, r *http.Request, leaf string) {
	if r.Method != http.MethodPost {
		rt.decoy.ServeHTTP(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	switch leaf {
	case "yookassa":
		if err := rt.mgr.HandleYooKassaWebhook(body); err != nil {
			log.Printf("[WARN] payment webhook (yookassa): %v", err)
		}
	case "cryptobot":
		if err := rt.mgr.HandleCryptoBotWebhook(body, r.Header.Get("crypto-pay-api-signature")); err != nil {
			log.Printf("[WARN] payment webhook (cryptobot): %v", err)
		}
	default:
		rt.decoy.ServeHTTP(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
