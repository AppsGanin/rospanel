package store

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestWebhookCRUD(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "wh.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	h, err := st.CreateWebhook("https://example.com/hook", []string{model.WebhookUserCreated})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.Secret == "" {
		t.Fatal("expected a generated secret")
	}
	if !h.Enabled {
		t.Fatal("new webhook should be enabled")
	}

	// Secret round-trips through the encrypted column.
	got, err := st.GetWebhook(h.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Secret != h.Secret {
		t.Fatalf("secret mismatch: %q vs %q", got.Secret, h.Secret)
	}
	if len(got.Events) != 1 || got.Events[0] != model.WebhookUserCreated {
		t.Fatalf("events not persisted: %+v", got.Events)
	}

	// Subscription filter: this hook wants only user.created.
	hooks, err := st.EnabledWebhooksForEvent(model.WebhookUserCreated)
	if err != nil || len(hooks) != 1 {
		t.Fatalf("EnabledWebhooksForEvent(created) = (%d, %v), want 1", len(hooks), err)
	}
	if hooks, _ := st.EnabledWebhooksForEvent(model.WebhookPaymentPaid); len(hooks) != 0 {
		t.Fatalf("hook should not match payment.paid, got %d", len(hooks))
	}

	// Disable → drops out of the fan-out set.
	if err := st.UpdateWebhook(h.ID, h.URL, h.Events, false); err != nil {
		t.Fatalf("update: %v", err)
	}
	if hooks, _ := st.EnabledWebhooksForEvent(model.WebhookUserCreated); len(hooks) != 0 {
		t.Fatalf("disabled hook still in fan-out set: %d", len(hooks))
	}

	// Empty events set ⇒ subscribed to everything.
	all, err := st.CreateWebhook("https://example.com/all", nil)
	if err != nil {
		t.Fatalf("create all: %v", err)
	}
	if hooks, _ := st.EnabledWebhooksForEvent(model.WebhookPaymentPaid); len(hooks) != 1 || hooks[0].ID != all.ID {
		t.Fatalf("empty-events hook should match any event")
	}

	if err := st.DeleteWebhook(all.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ := st.ListWebhooks()
	if len(list) != 1 {
		t.Fatalf("after delete want 1 webhook, got %d", len(list))
	}
}
