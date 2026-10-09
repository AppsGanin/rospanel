package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// A plugin can feed itself: onEvent changes a user through panel.api, the change
// is an event it is subscribed to, and so on. Until the actor travels with every
// event the breaker for that is a rate: past stormRate deliveries a minute while
// the plugin itself makes stormWrites changes through panel.api a minute, for
// stormMinutes minutes running, it is paused. Both
// sides of the loop are required: the panel's own bulk events — a monthly traffic
// reset for thousands of users — come fast too, but a plugin that only reads them
// is not feeding anything.
const (
	stormRate    = 300
	stormWrites  = 100
	stormMinutes = 3
)

type stormMeter struct {
	mu     sync.Mutex
	minute int64
	count  int // deliveries this minute
	writes int // the plugin's panel.api changes this minute
	hot    int // consecutive minutes over both rates
}

// roll moves the meter to the minute of now (call with mu held).
func (s *stormMeter) roll(now time.Time) {
	m := now.Unix() / 60
	switch m {
	case s.minute:
	case s.minute + 1:
		if s.count > stormRate && s.writes >= stormWrites {
			s.hot++
		} else {
			s.hot = 0
		}
		s.minute, s.count, s.writes = m, 0, 0
	default: // a quiet minute or more in between
		s.minute, s.count, s.writes, s.hot = m, 0, 0, 0
	}
}

// note counts one delivery and reports whether the plugin is in a storm.
func (s *stormMeter) note(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roll(now)
	s.count++
	return s.hot >= stormMinutes-1 && s.count > stormRate && s.writes >= stormWrites
}

// noteWrite counts one change the plugin made through panel.api.
func (s *stormMeter) noteWrite(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roll(now)
	s.writes++
}

// EventSubscribers lists the active plugins subscribed to event, in order: those
// that list it, and the delivery channels for the events that carry a message.
func (h *Host) EventSubscribers(event string) []string {
	return h.Active(func(m *manifest.Manifest) bool {
		return slices.Contains(m.Provides.Events, event) || m.Provides.Channel != nil && slices.Contains(channelEvents, event)
	})
}

// EventPlugins lists the active plugins that take events: subscribers and channels.
func (h *Host) EventPlugins() []string {
	return h.Active(func(m *manifest.Manifest) bool { return len(m.Provides.Events) > 0 || m.Provides.Channel != nil })
}

// DeliverEvent calls a plugin's onEvent with a stored webhook payload. gone is
// true when the plugin cannot take it any more and it should not be retried.
func (h *Host) DeliverEvent(ctx context.Context, id string, body []byte) (gone bool, err error) {
	gone, err = h.deliverEvent(ctx, id, body)
	if gone {
		// The panel shutting down is not the plugin going away: what it was handed
		// is delivered after the restart. A pause the panel retries keeps it too.
		if h.isClosed() {
			return false, ErrNotActive
		}
		if inst, e := h.get(id); e == nil && inst.retryAt.Load() != 0 {
			return false, err
		}
	}
	return gone, err
}

func (h *Host) deliverEvent(ctx context.Context, id string, body []byte) (gone bool, err error) {
	inst, err := h.get(id)
	if err != nil {
		return true, err
	}
	if inst.storm.note(time.Now()) {
		inst.mu.Lock()
		// Seen outside the lock: the operator may have switched the plugin off or
		// removed it meanwhile, and a pause must not switch it back on.
		if inst.removed || !inst.rec.Enabled || inst.status != model.PluginActive {
			inst.mu.Unlock()
			return true, ErrNotActive
		}
		inst.pause(fmt.Sprintf("an event storm: over %d events a minute for %d minutes — does onEvent trigger its own events?", stormRate, stormMinutes))
		inst.mu.Unlock()
		return true, errors.New("plugin paused: event storm")
	}
	p := inst.pub.Load()
	if p == nil || p.manifest == nil || p.status != model.PluginActive {
		return true, ErrNotActive
	}
	var ev struct {
		Event string `json:"event"`
	}
	_ = json.Unmarshal(body, &ev)
	if slices.Contains(p.manifest.Provides.Events, ev.Event) {
		_, err = h.call(ctx, id, "onEvent", json.RawMessage(body), EventTimeout, callOpts{})
	}
	if err == nil && p.manifest.Provides.Channel != nil && slices.Contains(channelEvents, ev.Event) {
		if msg := channelMessage(body); msg != nil {
			_, err = h.call(ctx, id, "channel.send", msg, ChannelTimeout, callOpts{})
		}
	}
	if errors.Is(err, ErrNotActive) || errors.Is(err, ErrNotFound) {
		return true, err
	}
	return false, err
}

// ChannelTimeout bounds one channel.send.
const ChannelTimeout = 15 * time.Second

// channelEvents carry something to tell a user: what a delivery channel takes.
var channelEvents = []string{
	model.WebhookUserMessage, model.WebhookUserAutoMessage, model.WebhookBroadcastSent,
	model.WebhookUserExpiring, model.WebhookUserTrafficLow,
}

// ChannelMessage is what channel.send receives: one message, for the users the
// panel's own bot did not reach.
type ChannelMessage struct {
	EventID string           `json:"event_id"` // for idempotency, as onEvent's e.id
	Kind    string           `json:"kind"`     // message | auto_message | broadcast | notice
	Notice  string           `json:"notice,omitempty"`
	Text    string           `json:"text,omitempty"` // Telegram HTML
	Buttons json.RawMessage  `json:"buttons,omitempty"`
	Users   []map[string]any `json:"users"`
	Data    json.RawMessage  `json:"data"` // the event's own payload
}

// channelMessage turns a stored event into a channel message, or nil when there is
// nobody to deliver it to: the bot delivered it already, or (a reminder) the user
// has a Telegram the bot warns through.
func channelMessage(body []byte) *ChannelMessage {
	var ev struct {
		ID    string          `json:"id"`
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &ev) != nil {
		return nil
	}
	var d map[string]any
	if json.Unmarshal(ev.Data, &d) != nil {
		return nil
	}
	msg := &ChannelMessage{EventID: ev.ID, Data: ev.Data}
	if b, err := json.Marshal(d["buttons"]); err == nil && d["buttons"] != nil {
		msg.Buttons = b
	}
	msg.Text, _ = d["text"].(string)
	userOf := func(d map[string]any) map[string]any {
		u := map[string]any{}
		for _, k := range []string{"id", "name", "external_id", "telegram_id", "lang", "mailing"} {
			if v, ok := d[k]; ok {
				u[k] = v
			}
		}
		return u
	}
	switch ev.Event {
	case model.WebhookUserMessage, model.WebhookUserAutoMessage:
		if sent, _ := d["telegram_sent"].(bool); sent {
			return nil
		}
		msg.Kind = "message"
		if ev.Event == model.WebhookUserAutoMessage {
			msg.Kind = "auto_message"
		}
		msg.Users = []map[string]any{userOf(d)}
	case model.WebhookBroadcastSent:
		msg.Kind = "broadcast"
		list, _ := d["users"].([]any)
		for _, x := range list {
			if u, ok := x.(map[string]any); ok {
				msg.Users = append(msg.Users, u)
			}
		}
		if len(msg.Users) == 0 {
			return nil
		}
	case model.WebhookUserExpiring, model.WebhookUserTrafficLow:
		if tg, _ := d["telegram_id"].(float64); tg != 0 {
			return nil
		}
		msg.Kind, msg.Notice = "notice", "expiring"
		if ev.Event == model.WebhookUserTrafficLow {
			msg.Notice = "traffic_low"
		}
		msg.Users = []map[string]any{userOf(d)}
	default:
		return nil
	}
	return msg
}
