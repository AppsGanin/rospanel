package core

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The webhook reminders go out at the expiry stages and the bot's quota share, once
// per stage of a term and once per quota, re-arm on a renewal and a reset, skip a disabled user, and record
// nothing while no webhook takes them.
func TestHookReminders(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "rem.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().Unix()
	day := int64(86400)
	m := &Manager{store: st, tz: time.UTC, webhookCh: make(chan webhookJob, 64),
		clock: func() time.Time { return time.Unix(now, 0) }}
	soon, err := st.CreateUser("soon", "uuid-soon", "pw", "tok-soon", 1000, now+2*day, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateTraffic(soon.ID, 400, 450, 0, 0); err != nil { // 85%
		t.Fatal(err)
	}
	off, err := st.CreateUser("off", "uuid-off", "pw", "tok-off", 1000, now+day, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateTraffic(off.ID, 900, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserEnabled(off.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser("later", "uuid-later", "pw", "tok-later", 0, now+20*day, 0); err != nil {
		t.Fatal(err)
	}

	pass := func() map[string][]map[string]any {
		t.Helper()
		users, err := m.enforcementUsers()
		if err != nil {
			t.Fatal(err)
		}
		set, _ := st.GetSettings()
		m.remindHooks(set, users)
		got := map[string][]map[string]any{}
		for _, job := range takeWebhooks(t, st) {
			var p struct {
				Event string         `json:"event"`
				Data  map[string]any `json:"data"`
			}
			if err := json.Unmarshal(job.Body, &p); err != nil {
				t.Fatal(err)
			}
			got[p.Event] = append(got[p.Event], p.Data)
		}
		return got
	}

	// No webhook takes them: nothing is sent, and nothing is recorded as sent.
	if got := pass(); len(got) != 0 {
		t.Fatalf("with no webhook: %v", got)
	}
	if u, _ := st.GetUser(soon.ID); u.HookExpireAt != 0 || u.HookQuotaAt != 0 {
		t.Fatalf("markers set with no webhook to send to: %d/%d", u.HookExpireAt, u.HookQuotaAt)
	}

	hook, err := st.CreateWebhook("https://hooks.example/site",
		[]string{model.WebhookUserExpiring, model.WebhookUserTrafficLow}, true)
	if err != nil {
		t.Fatal(err)
	}
	got := pass()
	if len(got[model.WebhookUserExpiring]) != 1 || len(got[model.WebhookUserTrafficLow]) != 1 {
		t.Fatalf("first pass: %v — want one of each, for %q only", got, "soon")
	}
	// Two days left: the 3-day stage, not the 14 and 7 it never reached.
	if e := got[model.WebhookUserExpiring][0]; e["name"] != "soon" || e["days_left"] != float64(2) || e["stage"] != float64(3) {
		t.Errorf("user.expiring = %v", e)
	}
	if l := got[model.WebhookUserTrafficLow][0]; l["name"] != "soon" || l["percent"] != float64(85) || l["used"] != float64(850) {
		t.Errorf("user.traffic_low = %v", l)
	}
	if got := pass(); len(got) != 0 {
		t.Fatalf("a second pass repeated them: %v", got)
	}

	// A renewal moves the term, a reset brings usage back under the line: both re-arm.
	if err := st.SetUserLimits(soon.ID, 1000, now+day, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.ResetTraffic(soon.ID, 0, 0, now); err != nil {
		t.Fatal(err)
	}
	got = pass()
	if len(got[model.WebhookUserExpiring]) != 1 || len(got[model.WebhookUserTrafficLow]) != 0 {
		t.Fatalf("after renewal and reset: %v — want the expiry reminder again, no traffic one", got)
	}
	if u, _ := st.GetUser(soon.ID); u.HookQuotaAt != 0 {
		t.Error("the traffic reminder was not re-armed by the reset")
	}
	if err := st.UpdateTraffic(soon.ID, 900, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := pass(); len(got[model.WebhookUserTrafficLow]) != 1 {
		t.Fatalf("over the line again after a reset: %v", got)
	}

	// The webhook switched off, they stop.
	if err := st.UpdateWebhook(hook.ID, hook.URL, hook.Events, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserLimits(soon.ID, 1000, now+2*day, 0); err != nil {
		t.Fatal(err)
	}
	if got := pass(); len(got) != 0 {
		t.Fatalf("reminders off: %v", got)
	}
}

// A term walks through the stages as it runs down: 14, then 7, 3 and 1 days ahead,
// one each, at the moment it reaches it.
func TestHookExpiringStages(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "stages.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	start := time.Now().Unix()
	now := start
	day := int64(86400)
	m := &Manager{store: st, tz: time.UTC, webhookCh: make(chan webhookJob, 1),
		clock: func() time.Time { return time.Unix(now, 0) }}
	if _, err := st.CreateWebhook("https://hooks.example/x", []string{model.WebhookUserExpiring}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser("term", "uuid-term", "pw", "tok-term", 0, start+20*day, 0); err != nil {
		t.Fatal(err)
	}
	stagesAt := func(at int64) []float64 {
		t.Helper()
		now = at
		users, err := m.enforcementUsers()
		if err != nil {
			t.Fatal(err)
		}
		set, _ := st.GetSettings()
		m.remindHooks(set, users)
		var out []float64
		for _, job := range takeWebhooks(t, st) {
			var p struct {
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal(job.Body, &p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p.Data["stage"].(float64))
		}
		return out
	}
	end := start + 20*day
	for _, c := range []struct {
		at   int64
		want []float64
	}{
		{end - 15*day, nil},
		{end - 14*day, []float64{14}},
		{end - 10*day, nil},
		{end - 7*day, []float64{7}},
		{end - 7*day + 60, nil},
		{end - 3*day, []float64{3}},
		{end - day, []float64{1}},
		{end - 60, nil},
	} {
		if got := stagesAt(c.at); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%d s before the end: stages %v, want %v", end-c.at, got, c.want)
		}
	}
}
