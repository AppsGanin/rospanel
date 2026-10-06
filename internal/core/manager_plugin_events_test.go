package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

type fakePlugins struct {
	mu    sync.Mutex
	subs  map[string][]string
	got   chan string // "plugin event-body"
	fail  map[string]int
	gone  map[string]bool
	sleep time.Duration
}

func (f *fakePlugins) EventSubscribers(event string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subs[event]
}

func (f *fakePlugins) DeliverEvent(_ context.Context, plugin string, body []byte) (bool, error) {
	time.Sleep(f.sleep)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone[plugin] {
		return true, errors.New("not active")
	}
	if f.fail[plugin] > 0 {
		f.fail[plugin]--
		return false, errors.New("boom")
	}
	f.got <- plugin + " " + string(body)
	return false, nil
}

func pluginEventsManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pe.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st, webhookCh: make(chan webhookJob, 8), webhookKick: make(chan struct{}, 1),
		pluginKick: make(chan struct{}, 1), done: make(chan struct{})}
	t.Cleanup(func() { close(m.done); m.wg.Wait() })
	return m, st
}

// Events reach subscribed plugins through the outbox, and a slow plugin does not
// hold an endpoint's webhook back.
func TestPluginEventsDeliveredApartFromWebhooks(t *testing.T) {
	m, st := pluginEventsManager(t)
	hooked := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hooked <- struct{}{} }))
	defer srv.Close()
	if _, err := st.CreateWebhook(srv.URL, []string{model.WebhookUserCreated}, true); err != nil {
		t.Fatal(err)
	}
	fp := &fakePlugins{subs: map[string][]string{model.WebhookUserCreated: {"slow"}}, got: make(chan string, 4), sleep: 2 * time.Second}
	m.SetPluginEvents(fp)
	m.startWebhookWorkers()
	m.startPluginEventWorkers()

	start := time.Now()
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 7})
	select {
	case <-hooked:
		if d := time.Since(start); d > time.Second {
			t.Fatalf("the webhook waited %v behind the plugin", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("webhook not delivered")
	}
	select {
	case got := <-fp.got:
		if got[:5] != "slow " {
			t.Fatal(got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("plugin event not delivered")
	}
}

// A failed delivery is retried; one to a plugin that is gone is dropped.
func TestPluginEventsRetryAndDrop(t *testing.T) {
	m, st := pluginEventsManager(t)
	fp := &fakePlugins{
		subs: map[string][]string{model.WebhookUserCreated: {"flaky", "off"}},
		got:  make(chan string, 4), fail: map[string]int{"flaky": 1}, gone: map[string]bool{"off": true},
	}
	m.SetPluginEvents(fp)
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 1})

	ch := make(chan pluginJob, 4)
	if m.dispatchPluginEvents(ch); len(ch) != 2 {
		t.Fatalf("leased %d deliveries, want 2", len(ch))
	}
	for len(ch) > 0 {
		m.deliverPluginEvent(<-ch)
	}
	// "off" is gone for good; "flaky" failed once and waits for its retry.
	n, _ := st.PendingWebhookDeliveries()
	if n != 1 {
		t.Fatalf("pending after the first round: %d, want 1", n)
	}
	ds, _ := st.LeasePluginDeliveries(time.Now().Add(time.Hour).Unix(), 0, 10)
	if len(ds) != 1 || ds[0].PluginID != "flaky" || ds[0].Attempt != 1 {
		t.Fatalf("%+v", ds)
	}
	m.deliverPluginEvent(pluginJob{outboxID: ds[0].ID, plugin: "flaky", body: ds[0].Body, attempt: 2})
	if got := <-fp.got; got[:6] != "flaky " {
		t.Fatal(got)
	}
	if n, _ := st.PendingWebhookDeliveries(); n != 0 {
		t.Fatalf("pending after the retry: %d", n)
	}

	// Stopping a plugin clears its queue.
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 2})
	m.DropPluginDeliveries("flaky")
	m.DropPluginDeliveries("off")
	if n, _ := st.PendingWebhookDeliveries(); n != 0 {
		t.Fatalf("pending after drop: %d", n)
	}
}

// Nothing is stored for an event no endpoint and no plugin wants.
func TestNoSubscribersNoRows(t *testing.T) {
	m, st := pluginEventsManager(t)
	m.SetPluginEvents(&fakePlugins{subs: map[string][]string{}})
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 1})
	if n, _ := st.PendingWebhookDeliveries(); n != 0 {
		t.Fatalf("%d rows for an unwanted event", n)
	}
}

// slowFirst sleeps on deliveries to "slow" and records when each delivery arrived.
type slowFirst struct {
	subs []string
	got  chan string
}

func (s *slowFirst) EventSubscribers(string) []string { return s.subs }

func (s *slowFirst) DeliverEvent(_ context.Context, plugin string, _ []byte) (bool, error) {
	if plugin == "slow" {
		time.Sleep(1500 * time.Millisecond)
	}
	s.got <- plugin
	return false, nil
}

// A plugin busy with one event does not get a second worker parked on it: the other
// plugin's event goes out meanwhile, and the busy one's waits its turn, unspent.
func TestBusyPluginDoesNotHoldBothWorkers(t *testing.T) {
	m, st := pluginEventsManager(t)
	fp := &slowFirst{subs: []string{"slow"}, got: make(chan string, 8)}
	m.SetPluginEvents(fp)
	// Two events for "slow" first, then one for "fast", all due at once.
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 1})
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 2})
	fp.subs = []string{"fast"}
	m.EmitWebhook(model.WebhookUserCreated, map[string]any{"id": 3})
	start := time.Now()
	m.startPluginEventWorkers()

	select {
	case p := <-fp.got:
		if p != "fast" {
			t.Fatalf("first delivery went to %s", p)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("fast waited %v behind the busy plugin", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing delivered")
	}
	// Both of slow's events still arrive, the second after the first.
	for i := 0; i < 2; i++ {
		select {
		case p := <-fp.got:
			if p != "slow" {
				t.Fatalf("got %s", p)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("slow's events were lost")
		}
	}
	ds, _ := st.LeasePluginDeliveries(time.Now().Add(time.Hour).Unix(), 0, 10)
	if len(ds) != 0 {
		t.Fatalf("%d deliveries left over", len(ds))
	}
}
