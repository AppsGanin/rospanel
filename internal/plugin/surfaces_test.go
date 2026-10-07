package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const surfacesManifest = `{
	"id": "surf", "version": "1.0.0", "api": 1, "name": {"ru": "Поверхности", "en": "Surfaces"},
	"provides": {
		"user_fields": [{"key": "email", "label": {"ru": "Почта", "en": "Email"}}, {"key": "score", "label": "Score"}],
		"actions": [
			{"key": "receipt", "label": "Receipt", "scope": "user"},
			{"key": "bulk", "label": "Bulk", "scope": "users", "perm": "billing.manage", "confirm": true},
			{"key": "sync", "label": "Sync", "scope": "global"}
		],
		"widgets": [{"key": "stat", "label": "Stat"}, {"key": "bad", "label": "Bad"}],
		"sub_blocks": true,
		"channel": {"label": "Mail"}
	},
	"experimental": ["channel"]
}`

const surfacesMain = `
let widgetCalls = 0;
export function userFields(id) { return id === 1 ? { email: "a@b.c", score: 42, extra: "dropped" } : {}; }
export function onAction(req) { panel.kv.set("last-action", req); return { ok: req.key !== "sync", message: "did " + req.key + " for " + req.user_ids.length }; }
export function widget(key) {
	widgetCalls++;
	panel.kv.set("widget-calls", widgetCalls);
	return key === "stat" ? { type: "stat", value: 7, hint: "seven" } : { type: "<script>" };
}
export function subBlocks(req) {
	return [
		{ type: "markdown", text: "**Hi** <script>x()</script> " + req.user.name },
		{ type: "button", label: "Pay", url: "https://pay.example/" + req.user.id },
		{ type: "button", label: "Evil", url: "javascript:alert(1)" },
		{ type: "iframe", text: "nope" },
	];
}
export const channel = { send(msg) { const n = panel.kv.get("sent") || []; n.push(msg); panel.kv.set("sent", n); return { delivered: msg.users.length }; } };
`

func installSurfaces(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.install(pkg(t, surfacesManifest, surfacesMain, nil))
	h.enable("surf")
	return h
}

func kvGet(t *testing.T, h *harness, key string) string {
	t.Helper()
	out, err := h.host.DevOp(context.Background(), "surf", "kv.get", []byte(`{"key":"`+key+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(out, &r)
	return r.Value
}

func TestUserFields(t *testing.T) {
	h := installSurfaces(t)
	got := h.host.UserFields(context.Background(), 1, "en")
	if len(got) != 1 || got[0].Name != "Surfaces" || len(got[0].Fields) != 2 ||
		got[0].Fields[0] != (FieldValue{"email", "Email", "a@b.c"}) || got[0].Fields[1].Value != "42" {
		t.Fatalf("%+v", got)
	}
	if got := h.host.UserFields(context.Background(), 2, "en"); len(got) != 0 {
		t.Fatalf("an empty answer made a block: %+v", got)
	}
}

func TestActionsAndPermissions(t *testing.T) {
	h := installSurfaces(t)
	all := func(string) bool { return true }
	usersOnly := func(p string) bool { return p == "users.manage" }
	if got := h.host.Actions("en", all); len(got) != 3 || got[0].Perm != "users.manage" || !got[1].Confirm {
		t.Fatalf("%+v", got)
	}
	if got := h.host.Actions("en", usersOnly); len(got) != 2 {
		t.Fatalf("an admin without billing.manage sees %+v", got)
	}
	ctx := context.Background()
	if _, _, err := h.host.RunAction(ctx, "surf", "bulk", []int64{1, 2}, usersOnly); !errors.Is(err, ErrForbidden) {
		t.Fatalf("pressed without its permission: %v", err)
	}
	res, _, err := h.host.RunAction(ctx, "surf", "bulk", []int64{1, 2}, all)
	if err != nil || !res.OK || res.Message != "did bulk for 2" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, _, err := h.host.RunAction(ctx, "surf", "receipt", []int64{1, 2}, all); err == nil {
		t.Fatal("a one-user action took two")
	}
	res, _, err = h.host.RunAction(ctx, "surf", "sync", []int64{9}, all)
	if err != nil || res.OK || res.Message != "did sync for 0" {
		t.Fatalf("a global action got users, or ok was lost: %+v %v", res, err)
	}
	if _, _, err := h.host.RunAction(ctx, "surf", "nope", nil, all); !errors.Is(err, ErrNoAction) {
		t.Fatal(err)
	}
}

func TestWidgetsCachedAndChecked(t *testing.T) {
	h := installSurfaces(t)
	ctx := context.Background()
	got := h.host.Widgets(ctx, "en")
	if len(got) != 2 || string(got[0].Data) != `{"type":"stat","value":7,"hint":"seven"}` || got[1].Error == "" || got[1].Data != nil {
		t.Fatalf("%+v", got)
	}
	h.host.Widgets(ctx, "en")
	if calls := kvGet(t, h, "widget-calls"); calls != "2" {
		t.Fatalf("the cache let %s calls through for two widgets", calls)
	}
}

func TestSubBlocksCleanedAndCached(t *testing.T) {
	h := installSurfaces(t)
	user := map[string]any{"id": 5, "name": "Ann"}
	got := h.host.SubBlocks(context.Background(), user, "ru")
	if len(got) != 2 || got[0].Type != "markdown" || got[1].URL != "https://pay.example/5" {
		t.Fatalf("%+v", got)
	}
	// The same user within the TTL is answered from the cache, even with the plugin off.
	_, _ = h.host.Disable(context.Background(), "surf")
	if again := h.host.SubBlocks(context.Background(), user, "ru"); len(again) != 0 {
		t.Fatalf("a switched-off plugin still adds blocks: %+v", again)
	}
}

func TestChannelMessage(t *testing.T) {
	ev := func(event, data string) []byte {
		return []byte(`{"id":"e1","event":"` + event + `","created_at":1,"data":` + data + `}`)
	}
	for _, c := range []struct {
		name, event, data string
		kind              string // "" = nothing to deliver
		users             int
	}{
		{"message the bot sent", "user.message", `{"id":1,"text":"hi","telegram_sent":true}`, "", 0},
		{"message the bot did not", "user.message", `{"id":1,"name":"A","text":"hi","buttons":[],"telegram_sent":false,"secret":"x"}`, "message", 1},
		{"auto message", "user.auto_message", `{"id":1,"text":"hi","telegram_sent":false}`, "auto_message", 1},
		{"broadcast", "broadcast.sent", `{"text":"news","users":[{"id":1,"external_id":"a"},{"id":2,"external_id":""}]}`, "broadcast", 2},
		{"broadcast to nobody", "broadcast.sent", `{"text":"news","users":[]}`, "", 0},
		{"reminder, Telegram user", "user.expiring", `{"id":1,"telegram_id":555,"days_left":3}`, "", 0},
		{"reminder, no Telegram", "user.traffic_low", `{"id":1,"telegram_id":0,"percent":90}`, "notice", 1},
		{"other event", "user.created", `{"id":1}`, "", 0},
	} {
		m := channelMessage(ev(c.event, c.data))
		if c.kind == "" {
			if m != nil {
				t.Errorf("%s: delivered %+v", c.name, m)
			}
			continue
		}
		if m == nil || m.Kind != c.kind || len(m.Users) != c.users || m.EventID != "e1" {
			t.Errorf("%s: %+v", c.name, m)
			continue
		}
		if _, leaked := m.Users[0]["secret"]; leaked {
			t.Errorf("%s: a user carries fields beyond the contact ones", c.name)
		}
	}
}

func TestChannelDelivery(t *testing.T) {
	h := installSurfaces(t)
	if subs := h.host.EventSubscribers("user.message"); len(subs) != 1 {
		t.Fatalf("a channel does not take user.message: %v", subs)
	}
	if subs := h.host.EventSubscribers("user.created"); len(subs) != 0 {
		t.Fatalf("a channel takes events it does not list: %v", subs)
	}
	body := []byte(`{"id":"e7","event":"user.message","created_at":1,"data":{"id":3,"text":"hello","telegram_sent":false}}`)
	if gone, err := h.host.DeliverEvent(context.Background(), "surf", body); gone || err != nil {
		t.Fatal(gone, err)
	}
	if sent := kvGet(t, h, "sent"); !strings.Contains(sent, `"kind":"message"`) || !strings.Contains(sent, `"text":"hello"`) {
		t.Fatalf("channel.send got %s", sent)
	}
}
