package core

import (
	"math"
	"testing"

	"github.com/AppsGanin/rospanel/internal/store"
)

// storedWebhook is one delivery a test found in the outbox.
type storedWebhook struct {
	Event string
	Body  []byte
}

// takeWebhooks empties the outbox the way a sender would — every delivery, in the
// order stored — and returns what it held, one entry per delivery per endpoint.
func takeWebhooks(t *testing.T, st *store.Store) []storedWebhook {
	t.Helper()
	ds, err := st.LeaseWebhookDeliveries(math.MaxInt32, 0, math.MaxInt32)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]storedWebhook, 0, len(ds))
	for _, d := range ds {
		if err := st.FinishWebhookDelivery(d.ID); err != nil {
			t.Fatal(err)
		}
		out = append(out, storedWebhook{Event: d.Event, Body: d.Body})
	}
	return out
}
