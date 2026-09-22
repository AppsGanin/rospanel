package core

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// enforcementRun is everything one pass of the stats poll's user handling leaves
// behind: what it told the admins, the users and the webhooks, what it wrote to the
// audit trail, and the users' rows afterwards.
type enforcementRun struct {
	Admin    []string
	User     []string
	Webhooks []string
	Events   []string
	Rows     []string
}

// seedEnforcement makes one user for every path the pass can take. The ids come out
// the same on every store it is run on.
func seedEnforcement(t *testing.T, st *store.Store, now int64) {
	t.Helper()
	if err := st.SetTelegramUserBot(true, "123:token", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserEvents(model.UserNotifyExpiring|model.UserNotifyExpired|model.UserNotifyTrafficLow|
		model.UserNotifyLimited|model.UserNotifyDeviceLimited|model.UserNotifyDisabled, 3); err != nil {
		t.Fatal(err)
	}
	var all int64
	for _, e := range model.AdminEventCatalog {
		all |= e.Bit
	}
	if err := st.SetAdminEvents(all); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWebhook("https://hooks.example/x",
		[]string{model.WebhookUserExpired, model.WebhookUserLimited, model.WebhookUserDeviceLimit}, true); err != nil {
		t.Fatal(err)
	}
	mk := func(name string, dataLimit, expireAt int64, deviceLimit int, chat int64) int64 {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw-"+name, "tok-"+name, dataLimit, expireAt, deviceLimit)
		if err != nil {
			t.Fatal(err)
		}
		if chat != 0 {
			if err := st.SetUserTelegramChat(u.ID, chat); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.SetNotifiedStatus(u.ID, model.StatusActive); err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	mk("expired", 0, now-3600, 0, 1001)
	limited := mk("limited", 1000, 0, 0, 1002)
	if err := st.UpdateTraffic(limited, 1500, 500, 0, 0); err != nil {
		t.Fatal(err)
	}
	crowded := mk("crowded", 0, 0, 1, 1003)
	for _, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		if err := st.AddConnection(crowded, ip, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.StampDeviceOverLimit(now - model.DeviceLimitGrace - 10); err != nil {
		t.Fatal(err)
	}
	off := mk("off", 0, 0, 0, 1004)
	if err := st.SetUserEnabled(off, false); err != nil {
		t.Fatal(err)
	}
	mk("expiring", 0, now+2*86400, 0, 1005)
	low := mk("low", 1000, 0, 0, 1006)
	if err := st.UpdateTraffic(low, 450, 400, 0, 0); err != nil {
		t.Fatal(err)
	}
	reset := mk("reset", 1000, 0, 0, 0)
	if err := st.UpdateTraffic(reset, 300, 200, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetResetPeriod(reset, "monthly", now-40*86400); err != nil {
		t.Fatal(err)
	}
	mk("plain", 0, 0, 0, 1008)
}

// enforcementReads is how a run reads its users: for the quota resets, then — after
// them, as the stats poll does — for the status and warning pass.
type enforcementReads struct {
	resets func(*Manager) ([]model.User, error)
	notify func(*Manager) ([]model.User, error)
}

// wholeReads reads every user in full for both.
var wholeReads = enforcementReads{
	resets: func(m *Manager) ([]model.User, error) { return m.store.ListUsers() },
	notify: func(m *Manager) ([]model.User, error) { return m.store.ListUsers() },
}

// runEnforcement seeds a fresh store at now and runs the pass over it. Runs compared
// with each other share now, so what they record carries the same times.
func runEnforcement(t *testing.T, now int64, seed func(*testing.T, *store.Store, int64), reads enforcementReads) enforcementRun {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "enforce.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	seed(t, st, now)
	// Every read judges users at now, however long the seeding took: the seed puts
	// users a minute either side of the edges, and a slow machine took longer than that.
	st.SetClock(func() int64 { return now })

	var run enforcementRun
	m := &Manager{store: st, tz: time.UTC, webhookCh: make(chan webhookJob, 4096), clock: func() time.Time { return time.Unix(now, 0) }}
	m.SetAdminNotifier(func(html string) { run.Admin = append(run.Admin, html) })
	m.SetUserNotifier(func(chat int64, html string) { run.User = append(run.User, fmt.Sprintf("%d:%s", chat, html)) })

	due, err := reads.resets(m)
	if err != nil {
		t.Fatal(err)
	}
	m.applyResets(due, now, func(id int64) (int64, int64) { return id * 10, id * 20 })
	users, err := reads.notify(m)
	if err != nil {
		t.Fatal(err)
	}
	m.notifyStatusTransitions(users)

	close(m.webhookCh)
	for job := range m.webhookCh {
		var p struct {
			Event string `json:"event"`
			Data  any    `json:"data"`
		}
		if err := json.Unmarshal(job.body, &p); err != nil {
			t.Fatal(err)
		}
		d, _ := json.Marshal(p.Data)
		run.Webhooks = append(run.Webhooks, p.Event+" "+string(d))
	}
	events, err := st.ListEvents(store.UserEventFilter{Limit: 5000})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		d, _ := json.Marshal(e.Details)
		run.Events = append(run.Events, fmt.Sprintf("%d %s %s %s", e.UserID, e.UserName, e.Action, d))
	}
	sort.Strings(run.Events)
	after, err := st.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range after {
		// Times written "now" by the pass are compared by whether they were set.
		run.Rows = append(run.Rows, fmt.Sprintf("%d %s notified=%s expireWarned=%v quotaWarned=%v used=%d/%d base=%d/%d reset=%v",
			u.ID, u.Name, u.NotifiedStatus, u.NotifiedExpireAt != 0, u.NotifiedQuotaAt != 0,
			u.UsedUp, u.UsedDown, u.LastUp, u.LastDown, u.LastResetAt > now-86400))
	}
	return run
}

// The stats poll and the enforcement pass read users through UserStatesByID, which
// leaves credentials, notes and bot fields out. Everything they do with those users
// must come out exactly as it would from whole users — the alerts, the users'
// messages, the webhooks, the audit trail and the rows. A later change that reads a
// field the narrow read does not carry shows up here as a difference.
func TestUserStatesServeEnforcementAsWholeUsers(t *testing.T) {
	t.Parallel()
	now := time.Now().Unix()
	whole := runEnforcement(t, now, seedEnforcement, wholeReads)
	states := runEnforcement(t, now, seedEnforcement, enforcementReads{
		resets: allUserStates,
		notify: allUserStates,
	})
	if !reflect.DeepEqual(whole, states) {
		t.Fatalf("the pass differs on narrow users:\nwhole:  %+v\nstates: %+v", whole, states)
	}
	// And the fixture exercises what it claims to, so the comparison is not of two
	// empty runs.
	if len(whole.Admin) != 3 || len(whole.User) != 6 || len(whole.Webhooks) != 3 || len(whole.Events) < 4 {
		t.Fatalf("fixture did not reach every path: %d alerts, %d user messages, %d webhooks, %d events\n%+v",
			len(whole.Admin), len(whole.User), len(whole.Webhooks), len(whole.Events), whole)
	}
}

// allUserStates reads every user as a state.
func allUserStates(m *Manager) ([]model.User, error) {
	users, err := m.store.ListUsers()
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	return m.store.UserStatesByID(ids)
}

// candidateReads reads what the stats poll and the enforcement pass read: the reset
// candidates, and the enforcement candidates.
var candidateReads = enforcementReads{
	resets: func(m *Manager) ([]model.User, error) {
		ids, err := m.store.ResetCandidates()
		if err != nil {
			return nil, err
		}
		return m.store.UserStatesByID(ids)
	},
	notify: func(m *Manager) ([]model.User, error) { return m.enforcementUsers() },
}

// seedRandomUsers makes users from every combination that matters to the pass, at
// random: expiries on either side of now and of the warning horizon, usage around the
// warning and the limit, device limits with stamps inside and past the grace and
// addresses in and out of the window, every notified status, warnings recorded or
// not, reset periods due and not. Times keep a minute clear of now, so the two runs a
// few milliseconds apart see the same statuses.
func seedRandomUsers(n int) func(*testing.T, *store.Store, int64) {
	return func(t *testing.T, st *store.Store, now int64) {
		t.Helper()
		if err := st.SetTelegramUserBot(true, "123:token", "", ""); err != nil {
			t.Fatal(err)
		}
		if err := st.SetUserEvents(model.UserNotifyExpiring|model.UserNotifyExpired|model.UserNotifyTrafficLow|
			model.UserNotifyLimited|model.UserNotifyDeviceLimited|model.UserNotifyDisabled, 3); err != nil {
			t.Fatal(err)
		}
		var all int64
		for _, e := range model.AdminEventCatalog {
			all |= e.Bit
		}
		if err := st.SetAdminEvents(all); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateWebhook("https://hooks.example/x",
			[]string{model.WebhookUserExpired, model.WebhookUserLimited, model.WebhookUserDeviceLimit}, true); err != nil {
			t.Fatal(err)
		}
		rng := rand.New(rand.NewPCG(20260915, uint64(n)))
		pick := func(options ...int64) int64 { return options[rng.IntN(len(options))] }
		day := int64(86400)
		type pending struct {
			id       int64
			stale    bool // addresses seen before the window
			pastDays bool // stamped past the grace rather than inside it
		}
		var crowded []pending
		for i := range n {
			name := fmt.Sprintf("r%d", i)
			expire := pick(0, 0, now-day, now-60, now+60, now+2*day, now+3*day-60, now+3*day+60, now+10*day)
			limit := pick(0, 0, 1000)
			devices := int(pick(0, 0, 1, 2))
			u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, limit, expire, devices)
			if err != nil {
				t.Fatal(err)
			}
			if used := pick(0, 500, 790, 800, 999, 1000, 3000); used > 0 {
				up := used / 3
				if err := st.UpdateTraffic(u.ID, up, used-up, 0, 0); err != nil {
					t.Fatal(err)
				}
			}
			if pick(0, 1) == 1 {
				if err := st.SetUserTelegramChat(u.ID, 5000+u.ID); err != nil {
					t.Fatal(err)
				}
			}
			if pick(0, 0, 0, 1) == 1 {
				if err := st.SetUserEnabled(u.ID, false); err != nil {
					t.Fatal(err)
				}
			}
			status := []string{"", model.StatusActive, model.StatusActive, model.StatusActive, model.StatusExpired,
				model.StatusLimited, model.StatusDeviceLimited, model.StatusDisabled}[rng.IntN(8)]
			if err := st.SetNotifiedStatus(u.ID, status); err != nil {
				t.Fatal(err)
			}
			switch pick(0, 1, 2) {
			case 1:
				if expire != 0 {
					if err := st.SetNotifiedExpireAt(u.ID, expire); err != nil {
						t.Fatal(err)
					}
				}
			case 2:
				if err := st.SetNotifiedExpireAt(u.ID, 12345); err != nil {
					t.Fatal(err)
				}
			}
			if pick(0, 1) == 1 {
				if err := st.SetNotifiedQuotaAt(u.ID, now-100); err != nil {
					t.Fatal(err)
				}
			}
			switch pick(0, 0, 1, 2, 3) {
			case 1:
				err = st.SetResetPeriod(u.ID, "monthly", now-40*day)
			case 2:
				err = st.SetResetPeriod(u.ID, "days:30", now-3600)
			case 3:
				err = st.SetResetPeriod(u.ID, "weekly", now-8*day)
			}
			if err != nil {
				t.Fatal(err)
			}
			if devices > 0 && pick(0, 1, 1) == 1 {
				crowded = append(crowded, pending{id: u.ID, stale: pick(0, 0, 1) == 1, pastDays: pick(0, 1) == 1})
			}
		}
		// Addresses for the crowded users, then the stamps: past the grace for some,
		// inside it for the others. A stale address still counts at its stamp but not
		// in the window now, so that user is stamped yet no longer over.
		for _, past := range []bool{true, false} {
			for _, c := range crowded {
				if c.pastDays != past {
					continue
				}
				seen := now
				if c.stale {
					seen = now - model.DeviceOnlineWindow - 60
				}
				for j := range 3 {
					if err := st.AddConnection(c.id, fmt.Sprintf("198.51.%d.%d", c.id%250, j+1), seen); err != nil {
						t.Fatal(err)
					}
				}
			}
			at := now - 60
			if past {
				at = now - model.DeviceLimitGrace - 60
			}
			if err := st.StampDeviceOverLimit(at); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// The enforcement pass reads only the candidates, and the stats poll only the users
// with a reset period. Over random users covering every case, what the pass does —
// every alert, message, webhook, audit row and row written — must be exactly what it
// does reading everyone.
func TestEnforcementCandidatesMissNobody(t *testing.T) {
	t.Parallel()
	const n = 400
	now := time.Now().Unix()
	// The two passes share nothing but the seed, so they run side by side: this is the
	// longest test in the package, and it was two of them back to back. The group's
	// t.Run returns only once both have finished.
	var whole, candidates enforcementRun
	t.Run("passes", func(t *testing.T) {
		t.Run("everyone", func(t *testing.T) {
			t.Parallel()
			whole = runEnforcement(t, now, seedRandomUsers(n), wholeReads)
		})
		t.Run("candidates", func(t *testing.T) {
			t.Parallel()
			candidates = runEnforcement(t, now, seedRandomUsers(n), candidateReads)
		})
	})
	if t.Failed() {
		return
	}
	if !reflect.DeepEqual(whole, candidates) {
		diff := func(name string, a, b []string) {
			if !reflect.DeepEqual(a, b) {
				t.Errorf("%s: %d reading everyone, %d reading candidates\n everyone:   %q\n candidates: %q", name, len(a), len(b), a, b)
			}
		}
		diff("admin alerts", whole.Admin, candidates.Admin)
		diff("user messages", whole.User, candidates.User)
		diff("webhooks", whole.Webhooks, candidates.Webhooks)
		diff("events", whole.Events, candidates.Events)
		diff("rows", whole.Rows, candidates.Rows)
		t.FailNow()
	}
	if len(whole.Admin) < 10 || len(whole.User) < 10 || len(whole.Webhooks) < 5 || len(whole.Events) < 10 {
		t.Fatalf("random users reached too few paths: %d alerts, %d messages, %d webhooks, %d events",
			len(whole.Admin), len(whole.User), len(whole.Webhooks), len(whole.Events))
	}
}
