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
	"github.com/AppsGanin/rospanel/internal/store"
)

// Outbound webhook delivery. When a lifecycle event fires (user created, payment
// paid, …) EmitWebhook fans it out to every enabled endpoint subscribed to that
// event: each delivery is an HMAC-SHA256-signed POST, retried a few times with a
// growing backoff. Deliveries wait in the webhook_outbox table rather than in memory,
// so a bulk action of thousands, or a restart, loses none of them; emitting stores
// the rows and returns — it never waits on an endpoint.
const (
	webhookWorkers     = 4
	webhookTimeout     = 10 * time.Second
	webhookMaxAttempts = 5
	// webhookLease keeps a delivery being sent from being taken again; past it, one
	// whose sender died with the panel is due once more. A batch drains in at most
	// webhookBatch/webhookWorkers timeouts (40 s), far inside it, so nothing leased is
	// still waiting for a worker when its lease runs out.
	webhookLease = 10 * time.Minute
	// webhookBatch is how many due deliveries the dispatcher leases at a time, and
	// webhookPoll how often it looks for retries coming due.
	webhookBatch = 16
	webhookPoll  = 2 * time.Second
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

// webhookJob is one delivery being sent: a signed body destined for one endpoint.
type webhookJob struct {
	outboxID int64 // its webhook_outbox row; 0 for a test delivery
	hookID   int64
	url      string
	secret   string
	event    string
	body     []byte // the exact bytes signed and POSTed
	attempt  int    // 1-based
}

// webhookPayload is the JSON envelope every delivery carries.
type webhookPayload struct {
	ID        string `json:"id"`    // unique delivery id (also the X-RosPanel-Delivery header)
	Event     string `json:"event"` // e.g. "user.created"
	CreatedAt int64  `json:"created_at"`
	Data      any    `json:"data"`
}

// userEventData is the compact user payload shared by the user.* events, with the ids
// an external system knows the user by: its own (external_id, "" when none) and the
// Telegram (telegram_id, 0 when none). Built before a deletion, since afterwards the
// external id is gone with the row.
func (m *Manager) userEventData(u model.User) map[string]any {
	return m.usersEventData([]model.User{u})[0]
}

// usersEventData is userEventData for many users, how each is reached read at once:
// besides the ids, whether mailings go to them (mailing) and their language (lang,
// "" when unknown).
func (m *Manager) usersEventData(users []model.User) []map[string]any {
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	contacts, err := m.store.UserContacts(ids)
	if err != nil {
		logErr("webhook: reading the external ids failed", "users", len(ids), "err", err)
	}
	out := make([]map[string]any, len(users))
	for i, u := range users {
		out[i] = map[string]any{
			"id":          u.ID,
			"name":        u.Name,
			"status":      u.Status,
			"enabled":     u.Enabled,
			"expire_at":   u.ExpireAt,
			"data_limit":  u.DataLimit,
			"plan_id":     u.PlanID,
			"external_id": contacts[u.ID].ExternalID,
			"telegram_id": u.TgChatID,
			"mailing":     !contacts[u.ID].MailingOff,
			"lang":        contactLang(contacts[u.ID]),
		}
	}
	return out
}

// emitUserWebhook sends an event about one user: the usual user fields plus what the
// event adds. The user is read afresh, so the fields are the state the event left.
func (m *Manager) emitUserWebhook(event string, userID int64, extra map[string]any) {
	u, err := m.store.GetUser(userID)
	if err != nil {
		return
	}
	d := m.userEventData(*u)
	for k, v := range extra {
		d[k] = v
	}
	m.EmitWebhook(event, d)
}

// emitUsersWebhook sends one event per user — each read afresh, the subscribers looked
// up once — with the same extra fields on each.
func (m *Manager) emitUsersWebhook(event string, ids []int64, extra map[string]any) {
	if len(ids) == 0 || !m.webhookWanted(event) {
		return
	}
	// Read in chunks: a reset anchored the same moment for a whole imported roster
	// can be tens of thousands of users, past SQLite's limit on query parameters.
	const chunk = 1000
	for start := 0; start < len(ids); start += chunk {
		part := ids[start:min(start+chunk, len(ids))]
		users, err := m.store.UsersByIDs(part)
		if err != nil {
			logErr("webhook: reading the users failed", "event", event, "users", len(part), "err", err)
			continue
		}
		items := make([]any, 0, len(users))
		for _, d := range m.usersEventData(users) {
			for k, v := range extra {
				d[k] = v
			}
			items = append(items, d)
		}
		m.EmitWebhookEach(event, items)
	}
}

// emitPaymentWebhook sends a payment.* event: the order's own fields, the user as the
// order left them (user, with the ids an external system knows them by) and their
// balance (user_balance_kop — the order's own balance_kop is the part of the price
// the balance covered), plus what the event adds.
func (m *Manager) emitPaymentWebhook(event string, order *model.PaymentOrder, extra map[string]any) {
	if !m.webhookWanted(event) {
		return
	}
	d := map[string]any{}
	if raw, err := json.Marshal(order); err == nil {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber() // ids and kopecks stay exact
		_ = dec.Decode(&d)
	}
	if u, err := m.store.GetUser(order.UserID); err == nil {
		d["user"] = m.userEventData(*u)
	}
	if wal, err := m.store.GetWalletLite(order.UserID); err == nil {
		d["user_balance_kop"] = wal.BalanceKop
	}
	for k, v := range extra {
		d[k] = v
	}
	m.EmitWebhook(event, d)
}

// emitChangeBought reports a paid change of plan as plan.changed, beside the order's
// payment.paid: the plan moved as surely as when an operator moves it.
func (m *Manager) emitChangeBought(order *model.PaymentOrder) {
	if order.Kind != model.OrderChange {
		return
	}
	m.emitUserWebhook(model.WebhookPlanChanged, order.UserID, map[string]any{
		"plan": m.PlanName(order.PlanID), "prev_plan": m.PlanName(order.ChangeFrom), "order_id": order.ID,
	})
}

// EmitWebhook fans an event out to all subscribed endpoints: one stored delivery per
// endpoint, sent by the workers. Best-effort and non-blocking: any error is logged,
// never surfaced to the caller.
func (m *Manager) EmitWebhook(event string, data any) {
	m.EmitWebhookEach(event, []any{data})
}

// EmitWebhookEach emits one delivery per item, looking the subscribers up ONCE and
// storing every delivery in one write — a bulk action of a thousand users is one
// insert, not a thousand, and none of it is dropped.
func (m *Manager) EmitWebhookEach(event string, items []any) {
	if m.webhookCh == nil || len(items) == 0 {
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
	// Sized by the items alone: append grows it per endpoint, and an allocation is never
	// sized by a product of two lengths.
	ds := make([]store.WebhookDelivery, 0, len(items))
	for _, data := range items {
		body, err := json.Marshal(webhookPayload{
			ID: randomHex(16), Event: event, CreatedAt: time.Now().Unix(), Data: data,
		})
		if err != nil {
			logErr("webhook: marshal failed", "event", event, "err", err)
			continue
		}
		for _, h := range hooks {
			ds = append(ds, store.WebhookDelivery{HookID: h.ID, Event: event, Body: body})
		}
	}
	if err := m.store.EnqueueWebhookDeliveries(ds); err != nil {
		logErr("webhook: storing deliveries failed", "event", event, "deliveries", len(ds), "err", err)
		return
	}
	select {
	case m.webhookKick <- struct{}{}:
	default: // the dispatcher is already woken
	}
}

// startWebhookWorkers launches the dispatcher and the delivery worker pool.
func (m *Manager) startWebhookWorkers() {
	for i := 0; i < webhookWorkers; i++ {
		m.runAsync(m.webhookWorker)
	}
	m.runAsync(m.webhookDispatcher)
}

// webhookDispatcher leases due deliveries from the outbox and hands them to the
// workers: at once when an event is stored, and every webhookPoll for the retries
// coming due and for what a restart left behind.
func (m *Manager) webhookDispatcher() {
	tick := time.NewTicker(webhookPoll)
	defer tick.Stop()
	for {
		more := true
		for more {
			more = m.dispatchWebhooks()
		}
		select {
		case <-m.done:
			return
		case <-m.webhookKick:
		case <-tick.C:
		}
	}
}

// dispatchWebhooks leases one batch and queues it to the workers, reporting whether
// the batch was full — more may be due. A delivery whose endpoint was deleted or
// switched off meanwhile is dropped.
func (m *Manager) dispatchWebhooks() bool {
	ds, err := m.store.LeaseWebhookDeliveries(time.Now().Unix(), int64(webhookLease.Seconds()), webhookBatch)
	if err != nil {
		logErr("webhook: leasing deliveries failed", "err", err)
		return false
	}
	if len(ds) == 0 {
		return false
	}
	hooks, err := m.store.ListWebhooks()
	if err != nil {
		logErr("webhook: reading endpoints failed", "err", err)
		return false
	}
	byID := make(map[int64]model.Webhook, len(hooks))
	for _, h := range hooks {
		byID[h.ID] = h
	}
	for _, d := range ds {
		h, ok := byID[d.HookID]
		if !ok || !h.Enabled {
			_ = m.store.FinishWebhookDelivery(d.ID)
			continue
		}
		job := webhookJob{
			outboxID: d.ID, hookID: h.ID, url: h.URL, secret: h.Secret,
			event: d.Event, body: d.Body, attempt: d.Attempt + 1,
		}
		select {
		case m.webhookCh <- job:
		case <-m.done:
			return false // the lease brings it back after the restart
		}
	}
	return len(ds) == webhookBatch
}

func (m *Manager) webhookWorker() {
	for {
		select {
		case <-m.done:
			// What is being sent is left leased in the outbox: it goes out again once
			// the lease runs out, after the restart, instead of holding shutdown open
			// for as long as the slowest endpoint takes to time out.
			return
		case job, ok := <-m.webhookCh:
			if !ok {
				return
			}
			m.deliverWebhook(job)
		}
	}
}

// deliverWebhook performs one delivery attempt: delivered or out of attempts, the
// outbox row goes; otherwise it is due again after a backoff. The outcome of every
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
	if err == nil || job.attempt >= webhookMaxAttempts {
		if err != nil {
			logWarn("webhook: giving up", "hook", job.hookID, "event", job.event,
				"attempts", job.attempt, "err", err)
		}
		if e := m.store.FinishWebhookDelivery(job.outboxID); e != nil {
			logErr("webhook: clearing a delivery failed", "hook", job.hookID, "err", e)
		}
		return
	}
	delay := webhookBackoff[len(webhookBackoff)-1]
	if job.attempt-1 < len(webhookBackoff) {
		delay = webhookBackoff[job.attempt-1]
	}
	if e := m.store.RetryWebhookDelivery(job.outboxID, job.attempt, time.Now().Add(delay).Unix()); e != nil {
		logErr("webhook: scheduling a retry failed", "hook", job.hookID, "err", e)
	}
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
