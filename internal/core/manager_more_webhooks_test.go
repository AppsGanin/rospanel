package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// What the bot would tell a Telegram user reaches an outside system too: the operator
// switching them off and on, one at a time or in bulk, and a balance correction.
func TestOperatorActionWebhooks(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "ops.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st, tz: time.UTC, webhookCh: make(chan webhookJob, 64)}
	if _, err := st.CreateWebhook("https://hooks.example/x", []string{
		model.WebhookUserEnabled, model.WebhookUserDisabled, model.WebhookBalanceAdjusted}, true); err != nil {
		t.Fatal(err)
	}
	drain := func() []string {
		t.Helper()
		var out []string
		for _, job := range takeWebhooks(t, st) {
			var p struct {
				Event string         `json:"event"`
				Data  map[string]any `json:"data"`
			}
			if err := json.Unmarshal(job.Body, &p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p.Event+" "+p.Data["name"].(string)+" "+p.Data["status"].(string))
		}
		sort.Strings(out)
		return out
	}
	ctx := context.Background()
	a, _ := st.CreateUser("ann", "uuid-ann", "pw", "tok-ann", 0, 0, 0)
	b, _ := st.CreateUser("bob", "uuid-bob", "pw", "tok-bob", 0, 0, 0)

	if err := m.SetUserEnabled(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetUserEnabled(ctx, a.ID, false); err != nil { // no change, no event
		t.Fatal(err)
	}
	if got := drain(); strings.Join(got, ",") != "user.disabled ann disabled" {
		t.Fatalf("disable: %q", got)
	}
	// Bulk: only the one actually switched is reported, with the status it now has.
	if _, err := m.BulkUserAction(ctx, []int64{a.ID, b.ID}, "enable", 0); err != nil {
		t.Fatal(err)
	}
	if got := drain(); strings.Join(got, ",") != "user.enabled ann active" {
		t.Fatalf("bulk enable: %q", got)
	}
	if _, err := m.BulkUserAction(ctx, []int64{a.ID, b.ID}, "disable", 0); err != nil {
		t.Fatal(err)
	}
	if got := drain(); strings.Join(got, ",") != "user.disabled ann disabled,user.disabled bob disabled" {
		t.Fatalf("bulk disable: %q", got)
	}
	if _, err := m.AdjustBalance(ctx, b.ID, 5000, "gift"); err != nil {
		t.Fatal(err)
	}
	if got := drain(); strings.Join(got, ",") != "balance.adjusted bob disabled" {
		t.Fatalf("balance: %q", got)
	}
}

// The term, the quota, the devices and the moderation queue reach an outside system
// as they change: limits set, traffic zeroed by hand and by the period, devices
// released, a sign-up filed and then approved — the approval naming its request.
func TestLimitsDevicesAndModerationWebhooks(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "lim.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st, tz: time.UTC, webhookCh: make(chan webhookJob, 64)}
	if _, err := st.CreateWebhook("https://hooks.example/x", nil, true); err != nil { // every event
		t.Fatal(err)
	}
	type ev struct {
		Event string         `json:"event"`
		Data  map[string]any `json:"data"`
	}
	drain := func() []ev {
		t.Helper()
		var out []ev
		for _, job := range takeWebhooks(t, st) {
			var p ev
			if err := json.Unmarshal(job.Body, &p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		return out
	}
	only := func(got []ev, event string) ev {
		t.Helper()
		if len(got) != 1 || got[0].Event != event {
			t.Fatalf("want one %s, got %+v", event, got)
		}
		return got[0]
	}
	ctx := context.Background()
	u, _ := st.CreateUser("lim", "uuid-lim", "pw", "tok-lim", 1000, 0, 0)

	if err := m.SetUserLimits(ctx, u.ID, 2000, time.Now().Add(48*time.Hour).Unix(), 3); err != nil {
		t.Fatal(err)
	}
	if e := only(drain(), model.WebhookUserLimitsChanged); e.Data["data_limit"] != float64(2000) {
		t.Errorf("limits: %+v", e.Data)
	}

	if err := st.UpdateTraffic(u.ID, 500, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.ResetTraffic(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if e := only(drain(), model.WebhookUserTrafficReset); e.Data["auto"] != false || e.Data["used_before"] != float64(500) {
		t.Errorf("manual reset: %+v", e.Data)
	}
	now := time.Now().Unix()
	if err := st.SetResetPeriod(u.ID, "daily", now-3*86400); err != nil {
		t.Fatal(err)
	}
	fresh, _ := st.GetUser(u.ID)
	m.applyResets([]model.User{*fresh}, now, func(int64) (int64, int64) { return 0, 0 })
	if e := only(drain(), model.WebhookUserTrafficReset); e.Data["auto"] != true {
		t.Errorf("period reset: %+v", e.Data)
	}

	if _, err := st.RegisterDevice(u.ID, model.Device{HWID: "hw-1", FirstSeen: now, LastSeen: now}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UnbindAllDevices(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if e := only(drain(), model.WebhookUserDeviceUnbound); e.Data["devices"] != float64(1) {
		t.Errorf("unbind: %+v", e.Data)
	}

	if ok, err := m.RequestRegistration(ctx, 777123, "applicant"); err != nil || !ok {
		t.Fatalf("request: %v %v", ok, err)
	}
	req := only(drain(), model.WebhookRegistrationRequested)
	if req.Data["telegram_id"] != float64(777123) {
		t.Errorf("requested: %+v", req.Data)
	}
	if err := m.ApproveRegistrationRequest(ctx, int64(req.Data["request_id"].(float64))); err != nil {
		t.Fatal(err)
	}
	var reg *ev
	for _, e := range drain() {
		if e.Event == model.WebhookUserRegistered {
			reg = &e
		}
	}
	if reg == nil || reg.Data["moderated"] != true || reg.Data["request_id"] != req.Data["request_id"] ||
		reg.Data["telegram_id"] != float64(777123) {
		t.Fatalf("approval: %+v", reg)
	}
}
