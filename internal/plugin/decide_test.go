package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

const gateManifest = `{
	"id": "gate", "version": "1.0.0", "api": 1, "name": "Gate",
	"permissions": ["users.view", "billing.manage"],
	"provides": {"hooks": ["beforeSignup", "beforeDeviceBind"], "price": true, "cron": [{"name": "slow", "schedule": "* * * * *"}]},
	"experimental": ["price"]
}`

const gateMain = `
export function beforeSignup(req) {
	let wrote = "no";
	try { panel.api("POST", "/v1/users", {}); wrote = "yes"; } catch (e) { wrote = "refused"; }
	panel.kv.set("wrote", wrote);
	panel.api("GET", "/v1/users/1");
	if (req.username === "spammer") return { allow: false, reason: "spam" };
	if (req.username === "bare") return false;
	if (req.username === "throws") throw new Error("broken");
	if (req.username === "loops") for (;;) {}
	return { allow: true };
}
export function beforeDeviceBind(req) {
	panel.kv.set("dev-calls", (panel.kv.get("dev-calls") || 0) + 1);
	return { allow: req.device_os !== "evil", reason: "Not this one" };
}
export function quotePrice(req) {
	if (req.plan === "low") return { price_rub: 1 };
	if (req.plan === "high") return { price_rub: req.base_rub + 1 };
	if (req.plan === "none") return null;
	return { price_rub: req.base_rub - 10, note: "regional" };
}
export function slow() { const end = Date.now() + 1500; while (Date.now() < end) {} }
`

func installGate(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.install(pkg(t, gateManifest, gateMain, map[string]string{
		"i18n/en.json": `{"spam": "Sign-ups like this are closed", "regional": "Regional price"}`,
		"i18n/ru.json": `{"spam": "Такие регистрации закрыты", "regional": "Региональная цена"}`,
	}))
	h.enable("gate")
	return h
}

func gateKV(t *testing.T, h *harness, key string) string {
	t.Helper()
	return gateKVOf(t, h, "gate", key)
}

func gateKVOf(t *testing.T, h *harness, id, key string) string {
	t.Helper()
	out, err := h.host.DevOp(context.Background(), id, "kv.get", []byte(`{"key":"`+key+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(out, &r)
	return r.Value
}

func TestBeforeSignup(t *testing.T) {
	h := installGate(t)
	ctx := context.Background()
	if !h.host.Hooked("beforeSignup") || h.host.Hooked("transformSubscription") {
		t.Fatal("Hooked")
	}
	if ok, _ := h.host.BeforeSignup(ctx, model.SignupCheck{Channel: "web", Username: "ann"}); !ok {
		t.Fatal("ann refused")
	}
	if got := gateKV(t, h, "wrote"); got != `"refused"` {
		t.Fatalf("a hook wrote through panel.api: %s", got)
	}
	ok, reason := h.host.BeforeSignup(ctx, model.SignupCheck{Username: "spammer", Lang: "ru"})
	if ok || reason != "Такие регистрации закрыты" {
		t.Fatalf("spammer: %v %q", ok, reason)
	}
	if ok, reason := h.host.BeforeSignup(ctx, model.SignupCheck{Username: "bare"}); ok || reason != "" {
		t.Fatalf("bare false: %v %q", ok, reason)
	}
	// Fails open: an error, and a call past the hook's time.
	if ok, _ := h.host.BeforeSignup(ctx, model.SignupCheck{Username: "throws"}); !ok {
		t.Fatal("a throwing hook refused")
	}
	start := time.Now()
	if ok, _ := h.host.BeforeSignup(ctx, model.SignupCheck{Username: "loops"}); !ok {
		t.Fatal("a looping hook refused")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("a looping hook held the sign-up for %v", d)
	}
}

func TestDecisionDoesNotWaitForABusyPlugin(t *testing.T) {
	h := installGate(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = h.host.Call(context.Background(), "gate", "slow", nil, 5*time.Second)
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	ok, _ := h.host.BeforeSignup(context.Background(), model.SignupCheck{Username: "spammer"})
	if !ok || time.Since(start) > time.Second {
		t.Fatalf("waited %v for a busy plugin (allow=%v)", time.Since(start), ok)
	}
	<-done
}

func TestBeforeDeviceBindCachesARefusal(t *testing.T) {
	h := installGate(t)
	ctx := context.Background()
	evil := model.DeviceCheck{UserID: 1, HWID: "a", DeviceOS: "evil", IP: "1.2.3.4"}
	for range 3 {
		if ok, reason := h.host.BeforeDeviceBind(ctx, evil); ok || reason != "Not this one" {
			t.Fatalf("%v %q", ok, reason)
		}
	}
	if ok, _ := h.host.BeforeDeviceBind(ctx, model.DeviceCheck{UserID: 1, HWID: "b", DeviceOS: "ios"}); !ok {
		t.Fatal("ios refused")
	}
	if got := gateKV(t, h, "dev-calls"); got != "2" {
		t.Fatalf("plugin asked %s times, want 2 (the refusal is kept)", got)
	}
}

func TestQuotePriceBounds(t *testing.T) {
	h := installGate(t)
	ctx := context.Background()
	price, note, ok := h.host.QuotePrice(ctx, model.PriceRequest{UserID: 1, PlanID: 1, Plan: "std", BaseRub: 300, Lang: "en"})
	if !ok || price != 290 || note != "Regional price" {
		t.Fatalf("%d %q %v", price, note, ok)
	}
	for i, plan := range []string{"low", "high", "none"} {
		if _, _, ok := h.host.QuotePrice(ctx, model.PriceRequest{UserID: 1, PlanID: int64(2 + i), Plan: plan, BaseRub: 300}); ok {
			t.Fatalf("%s: a price outside the bounds counted", plan)
		}
	}
	logs, _ := h.host.Logs("gate")
	var warned int
	for _, l := range logs {
		if strings.Contains(l.Msg, "is outside") {
			warned++
		}
	}
	if warned != 2 {
		t.Fatalf("want 2 warnings for out-of-bounds prices, got %d: %+v", warned, logs)
	}
}
