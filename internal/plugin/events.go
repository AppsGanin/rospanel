package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// A plugin can feed itself: onEvent changes a user through panel.api, the change
// is an event it is subscribed to, and so on. Until the actor travels with every
// event (docs/plugins-design.md, section 13) the breaker for that is a rate: past
// stormRate deliveries a minute for stormMinutes minutes running, the plugin is
// paused. Ordinary traffic is far below it — events follow what people do.
const (
	stormRate    = 300
	stormMinutes = 3
)

type stormMeter struct {
	mu     sync.Mutex
	minute int64
	count  int
	hot    int // consecutive minutes over the rate
}

// note counts one delivery and reports whether the plugin is in a storm.
func (s *stormMeter) note(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := now.Unix() / 60
	switch m {
	case s.minute:
	case s.minute + 1:
		if s.count > stormRate {
			s.hot++
		} else {
			s.hot = 0
		}
		s.minute, s.count = m, 0
	default: // a quiet minute or more in between
		s.minute, s.count, s.hot = m, 0, 0
	}
	s.count++
	return s.hot >= stormMinutes-1 && s.count > stormRate
}

// EventSubscribers lists the active plugins subscribed to event, in order.
func (h *Host) EventSubscribers(event string) []string {
	return h.Active(func(m *manifest.Manifest) bool { return slices.Contains(m.Provides.Events, event) })
}

// DeliverEvent calls a plugin's onEvent with a stored webhook payload. gone is
// true when the plugin cannot take it any more and it should not be retried.
func (h *Host) DeliverEvent(ctx context.Context, id string, body []byte) (gone bool, err error) {
	inst, err := h.get(id)
	if err != nil {
		return true, err
	}
	if inst.storm.note(time.Now()) {
		inst.mu.Lock()
		inst.pause(fmt.Sprintf("an event storm: over %d events a minute for %d minutes — does onEvent trigger its own events?", stormRate, stormMinutes))
		inst.mu.Unlock()
		return true, errors.New("plugin paused: event storm")
	}
	_, err = h.call(ctx, id, "onEvent", json.RawMessage(body), EventTimeout, callOpts{})
	if errors.Is(err, ErrNotActive) || errors.Is(err, ErrNotFound) {
		return true, err
	}
	return false, err
}
