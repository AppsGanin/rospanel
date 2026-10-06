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

// TestWebhookDeliverySigned drives the full path: EmitWebhook → outbox → worker →
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

	h, err := st.CreateWebhook(srv.URL, []string{model.WebhookUserCreated}, true)
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	m := &Manager{store: st, webhookCh: make(chan webhookJob, 8), webhookKick: make(chan struct{}, 1)}
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

	if _, err := st.CreateWebhook(srv.URL, []string{model.WebhookPaymentPaid}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	m := &Manager{store: st, webhookCh: make(chan webhookJob, 8), webhookKick: make(chan struct{}, 1)}
	m.startWebhookWorkers()
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 1})

	time.Sleep(300 * time.Millisecond)
	if n := hits.Load(); n != 0 {
		t.Fatalf("endpoint was called %d times for an unsubscribed event", n)
	}
}

// A bulk action's events all wait in the outbox, however many: the old in-memory
// queue dropped everything past its 512 slots.
func TestWebhookOutboxKeepsABulkWhole(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "bulk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for range 2 {
		if _, err := st.CreateWebhook("https://hooks.example/x", nil, true); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{store: st, webhookCh: make(chan webhookJob, 1)}
	items := make([]any, 3000)
	for i := range items {
		items[i] = map[string]any{"id": i}
	}
	m.EmitWebhookEach(model.WebhookUserDisabled, items)
	if n, _ := st.PendingWebhookDeliveries(); n != 6000 {
		t.Fatalf("pending = %d, want 6000 (3000 events × 2 endpoints)", n)
	}
}

// A failed delivery stays in the outbox, due again after the backoff; out of
// attempts it goes. A delivery for an endpoint switched off meanwhile is dropped.
func TestWebhookOutboxRetriesAndDrops(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	h, err := st.CreateWebhook(srv.URL, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{store: st, webhookCh: make(chan webhookJob, webhookBatch)}
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 1})
	if m.dispatchWebhooks() {
		t.Fatal("a batch of one reported more to come")
	}
	job := <-m.webhookCh
	if job.attempt != 1 || job.outboxID == 0 {
		t.Fatalf("job = %+v", job)
	}
	// Leased: not due again while it is being sent.
	if m.dispatchWebhooks(); len(m.webhookCh) != 0 {
		t.Fatal("a leased delivery was handed out twice")
	}
	m.deliverWebhook(job)
	ds, _ := st.LeaseWebhookDeliveries(time.Now().Add(time.Hour).Unix(), 0, 10)
	if len(ds) != 1 || ds[0].Attempt != 1 {
		t.Fatalf("after a failure: %+v", ds)
	}
	job.attempt = webhookMaxAttempts
	m.deliverWebhook(job)
	if n, _ := st.PendingWebhookDeliveries(); n != 0 {
		t.Fatalf("out of attempts, %d deliveries left", n)
	}

	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 2})
	if err := st.UpdateWebhook(h.ID, h.URL, nil, false); err != nil {
		t.Fatal(err)
	}
	m.dispatchWebhooks()
	if len(m.webhookCh) != 0 {
		t.Fatal("a delivery went to a switched-off endpoint")
	}
	if n, _ := st.PendingWebhookDeliveries(); n != 0 {
		t.Fatalf("the switched-off endpoint's delivery stayed: %d", n)
	}
}
