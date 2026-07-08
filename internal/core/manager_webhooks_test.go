package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// TestWebhookDeliverySigned drives the full path: EmitWebhook → queue → worker →
// signed POST → the receiver verifies the HMAC signature. The receiver runs on
// 127.0.0.1, which is only reachable because webhook delivery deliberately does
// not apply the SSRF private-host guard.
func TestWebhookDeliverySigned(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "wh.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	type capture struct {
		event, sig string
		body       []byte
	}
	got := make(chan capture, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- capture{
			event: r.Header.Get("X-RosPanel-Event"),
			sig:   r.Header.Get("X-RosPanel-Signature"),
			body:  b,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h, err := st.CreateWebhook(srv.URL, []string{model.WebhookUserCreated})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	m := &Manager{store: st, webhookCh: make(chan webhookJob, 8)}
	m.startWebhookWorkers()
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 7, "name": "alice"})

	var c capture
	select {
	case c = <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("webhook was not delivered")
	}

	if c.event != model.WebhookUserCreated {
		t.Errorf("X-RosPanel-Event = %q, want %q", c.event, model.WebhookUserCreated)
	}
	// Recompute the signature the way a receiver would.
	mac := hmac.New(sha256.New, []byte(h.Secret))
	mac.Write(c.body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if c.sig != want {
		t.Errorf("signature mismatch:\n got %q\nwant %q", c.sig, want)
	}
	// Payload envelope carries the event + data.
	var payload webhookPayload
	if err := json.Unmarshal(c.body, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload.Event != model.WebhookUserCreated {
		t.Errorf("payload.event = %q", payload.Event)
	}

	// The delivery result is recorded on the endpoint (status 200).
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec, _ := st.GetWebhook(h.ID)
		if rec != nil && rec.LastStatus == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("last_status was not recorded as 200")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestWebhookNonSubscribed verifies an endpoint only wired for payment events is
// not called for a user event.
func TestWebhookNonSubscribed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "wh2.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if _, err := st.CreateWebhook(srv.URL, []string{model.WebhookPaymentPaid}); err != nil {
		t.Fatalf("create: %v", err)
	}
	m := &Manager{store: st, webhookCh: make(chan webhookJob, 8)}
	m.startWebhookWorkers()
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 1})

	time.Sleep(300 * time.Millisecond)
	if n := hits.Load(); n != 0 {
		t.Fatalf("endpoint was called %d times for an unsubscribed event", n)
	}
}
