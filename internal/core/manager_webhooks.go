package core

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Outbound webhook delivery. When a lifecycle event fires (user created, payment
// paid, …) EmitWebhook fans it out to every enabled endpoint subscribed to that
// event: each delivery is an HMAC-SHA256-signed POST, retried a few times with a
// growing backoff, sent through the SSRF-safe client (https-only, no private
// hosts). Delivery is fully asynchronous — emitting an event never blocks or fails
// the operation that produced it.
const (
	webhookQueueSize   = 512
	webhookWorkers     = 4
	webhookTimeout     = 10 * time.Second
	webhookMaxAttempts = 5
)

// webhookBackoff is the delay before attempt N+1 (index 0 is the wait after the
// first failure). The last value repeats if attempts run past it.
var webhookBackoff = []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// webhookClient delivers every webhook POST. Redirects are capped (an endpoint
// that bounces us forever is a failure, not a chase); the timeout bounds a slow
// receiver so it can't tie up a worker.
var webhookClient = &http.Client{
	Timeout: webhookTimeout,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// webhookJob is one queued delivery: a signed body destined for one endpoint.
type webhookJob struct {
	hookID  int64
	url     string
	secret  string
	event   string
	body    []byte // the exact bytes signed and POSTed
	attempt int    // 1-based
}

// webhookPayload is the JSON envelope every delivery carries.
type webhookPayload struct {
	ID        string `json:"id"`    // unique delivery id (also the X-RosPanel-Delivery header)
	Event     string `json:"event"` // e.g. "user.created"
	CreatedAt int64  `json:"created_at"`
	Data      any    `json:"data"`
}

// userEventData is the compact user payload shared by the user.* events.
func userEventData(u model.User) map[string]any {
	return map[string]any{
		"id":         u.ID,
		"name":       u.Name,
		"status":     u.Status,
		"enabled":    u.Enabled,
		"expire_at":  u.ExpireAt,
		"data_limit": u.DataLimit,
		"plan_id":    u.PlanID,
	}
}

// EmitWebhook fans an event out to all subscribed endpoints. It builds the signed
// body once per endpoint (each has its own secret) and enqueues the deliveries.
// Best-effort and non-blocking: any error is logged, never surfaced to the caller.
func (m *Manager) EmitWebhook(event string, data any) {
	if m.webhookCh == nil {
		return
	}
	hooks, err := m.store.EnabledWebhooksForEvent(event)
	if err != nil {
		logErr("webhook: lookup failed", "event", event, "err", err)
		return
	}
	if len(hooks) == 0 {
		return
	}
	deliveryID := randomHex(16)
	payload := webhookPayload{
		ID:        deliveryID,
		Event:     event,
		CreatedAt: time.Now().Unix(),
		Data:      data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		logErr("webhook: marshal failed", "event", event, "err", err)
		return
	}
	for _, h := range hooks {
		m.enqueueWebhook(webhookJob{
			hookID: h.ID, url: h.URL, secret: h.Secret,
			event: event, body: body, attempt: 1,
		})
	}
}

// enqueueWebhook pushes a job onto the queue without blocking; a full queue drops
// the delivery (logged) rather than stalling the emitting goroutine.
func (m *Manager) enqueueWebhook(job webhookJob) {
	select {
	case m.webhookCh <- job:
	default:
		logWarn("webhook: queue full, dropping delivery", "hook", job.hookID, "event", job.event)
	}
}

// startWebhookWorkers launches the delivery worker pool.
func (m *Manager) startWebhookWorkers() {
	for i := 0; i < webhookWorkers; i++ {
		go m.webhookWorker()
	}
}

func (m *Manager) webhookWorker() {
	for job := range m.webhookCh {
		m.deliverWebhook(job)
	}
}

// deliverWebhook performs one delivery attempt and, on failure, schedules the
// next attempt after a backoff (until webhookMaxAttempts). The outcome of every
// attempt is recorded on the endpoint for the settings UI.
func (m *Manager) deliverWebhook(job webhookJob) {
	status, err := m.postWebhook(job)
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	if e := m.store.MarkWebhookResult(job.hookID, status, errStr); e != nil {
		logErr("webhook: record result failed", "hook", job.hookID, "err", e)
	}
	if err == nil {
		return
	}
	if job.attempt >= webhookMaxAttempts {
		logWarn("webhook: giving up", "hook", job.hookID, "event", job.event,
			"attempts", job.attempt, "err", err)
		return
	}
	delay := webhookBackoff[len(webhookBackoff)-1]
	if job.attempt-1 < len(webhookBackoff) {
		delay = webhookBackoff[job.attempt-1]
	}
	next := job
	next.attempt++
	// Re-enqueue after the backoff. time.AfterFunc keeps the worker free to drain
	// other deliveries while this one waits.
	time.AfterFunc(delay, func() { m.enqueueWebhook(next) })
}

// postWebhook signs and sends one HTTP POST. It returns the HTTP status (0 on a
// connection-level error) and a non-nil error for any non-2xx or transport
// failure, so the caller knows whether to retry.
func (m *Manager) postWebhook(job webhookJob) (int, error) {
	if err := model.ValidWebhookURL(job.url); err != nil {
		return 0, err // re-checked each attempt in case the row was edited meanwhile
	}
	req, err := http.NewRequest(http.MethodPost, job.url, bytes.NewReader(job.body))
	if err != nil {
		return 0, err
	}
	sig := hmacHex(job.secret, job.body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RosPanel-Webhook/1")
	req.Header.Set("X-RosPanel-Event", job.event)
	req.Header.Set("X-RosPanel-Signature", "sha256="+sig)

	resp, err := webhookClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, errHTTPStatus(resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// TestWebhook sends a synchronous "ping" delivery to one endpoint and returns the
// HTTP status, for the "Test" button in the settings UI. It does not retry.
func (m *Manager) TestWebhook(id int64) (int, error) {
	h, err := m.store.GetWebhook(id)
	if err != nil {
		return 0, err
	}
	payload := webhookPayload{
		ID:        randomHex(16),
		Event:     "ping",
		CreatedAt: time.Now().Unix(),
		Data:      map[string]any{"message": "RosPanel webhook test"},
	}
	body, _ := json.Marshal(payload)
	status, err := m.postWebhook(webhookJob{
		hookID: h.ID, url: h.URL, secret: h.Secret, event: "ping", body: body, attempt: 1,
	})
	_ = m.store.MarkWebhookResult(h.ID, status, errString(err))
	return status, err
}

// hmacHex returns the hex HMAC-SHA256 of body under secret — the value sent in
// X-RosPanel-Signature (as "sha256=<hex>") and what a receiver recomputes to
// verify the payload.
func hmacHex(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "0"
	}
	return hex.EncodeToString(b)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// errHTTPStatus wraps a non-2xx response code as a retriable error.
func errHTTPStatus(code int) error { return fmt.Errorf("HTTP %d", code) }
