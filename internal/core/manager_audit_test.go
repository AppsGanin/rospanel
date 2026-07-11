package core

import (
	"context"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/model"
)

// adminCtx is a request context as the panel's requireAuth would stamp it.
func adminCtx() context.Context {
	return actor.With(context.Background(), actor.Admin("root"))
}

// trail returns a user's audit actions, newest first.
func trail(t *testing.T, m *Manager, userID int64) []model.UserEvent {
	t.Helper()
	events, err := m.UserEvents(userID, 100, 0)
	if err != nil {
		t.Fatalf("UserEvents: %v", err)
	}
	return events
}

// actions flattens a trail to its action keys.
func actions(events []model.UserEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Action)
	}
	return out
}

func hasAction(events []model.UserEvent, action string) bool {
	for _, e := range events {
		if e.Action == action {
			return true
		}
	}
	return false
}

// The actor stamped on the context must reach the audit row — that attribution is
// the whole point of threading it through the Manager.
func TestAuditRecordsActor(t *testing.T) {
	m := bulkTestManager(t)
	u, err := m.CreateUser(adminCtx(), "Вася", 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	events := trail(t, m, u.ID)
	if len(events) != 1 {
		t.Fatalf("want 1 event after create, got %v", actions(events))
	}
	e := events[0]
	if e.Action != model.EventUserCreated {
		t.Errorf("action = %q, want %q", e.Action, model.EventUserCreated)
	}
	if e.ActorKind != model.ActorAdmin || e.ActorName != "root" {
		t.Errorf("actor = %s/%s, want admin/root", e.ActorKind, e.ActorName)
	}
	if e.UserName != "Вася" {
		t.Errorf("user_name = %q, want Вася", e.UserName)
	}
}

// A context with no actor is the panel acting on its own — the background poller,
// a provider webhook — and must record as system, not as an empty actor.
func TestAuditDefaultsToSystem(t *testing.T) {
	m := bulkTestManager(t)
	u, _ := m.CreateUser(context.Background(), "bot", 0, 0)
	events := trail(t, m, u.ID)
	if len(events) == 0 || events[0].ActorKind != model.ActorSystem {
		t.Fatalf("want a system-attributed row, got %+v", events)
	}
}

// Each single-user mutation writes its own row, so the trail reads as a history.
func TestAuditUserLifecycle(t *testing.T) {
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "Вася", 0, 0)

	if err := m.RenameUser(ctx, u.ID, "Пётр"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := m.SetUserEnabled(ctx, u.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := m.SetUserLimits(ctx, u.ID, 1024, 0, 2); err != nil {
		t.Fatalf("limits: %v", err)
	}
	if err := m.SetResetPeriod(ctx, u.ID, "daily"); err != nil {
		t.Fatalf("reset period: %v", err)
	}

	events := trail(t, m, u.ID)
	for _, want := range []string{
		model.EventUserCreated, model.EventUserRenamed, model.EventUserDisabled,
		model.EventUserLimits, model.EventResetPeriod,
	} {
		if !hasAction(events, want) {
			t.Errorf("missing %q in trail %v", want, actions(events))
		}
	}

	// The rename row must carry both names — the old one exists nowhere else.
	for _, e := range events {
		if e.Action != model.EventUserRenamed {
			continue
		}
		d, ok := e.Details.(map[string]any)
		if !ok || d["from"] != "Вася" || d["to"] != "Пётр" {
			t.Errorf("rename details = %#v, want from=Вася to=Пётр", e.Details)
		}
	}
}

// Deleting a user keeps their trail, and the deletion row keeps the name — the row
// can't look it up after the fact.
func TestAuditSurvivesDelete(t *testing.T) {
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "Вася", 0, 0)
	if err := m.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	events := trail(t, m, u.ID)
	if !hasAction(events, model.EventUserDeleted) {
		t.Fatalf("no delete row in trail %v", actions(events))
	}
	for _, e := range events {
		if e.Action == model.EventUserDeleted && e.UserName != "Вася" {
			t.Errorf("delete row lost the name: %+v", e)
		}
	}
}

// A bulk action writes one row per affected user, flagged as bulk, so a mass change
// still shows up in each individual user's trail.
func TestAuditBulkPerUser(t *testing.T) {
	m := bulkTestManager(t)
	ctx := adminCtx()
	a := mkUser(t, m, "a", 0)
	b := mkUser(t, m, "b", 0)

	n, err := m.BulkUserAction(ctx, []int64{a, b}, "disable", 0)
	if err != nil || n != 2 {
		t.Fatalf("bulk disable: n=%d err=%v", n, err)
	}
	for _, id := range []int64{a, b} {
		events := trail(t, m, id)
		if !hasAction(events, model.EventUserDisabled) {
			t.Fatalf("user %d: no disable row in %v", id, actions(events))
		}
		for _, e := range events {
			if e.Action != model.EventUserDisabled {
				continue
			}
			d, _ := e.Details.(map[string]any)
			if d["bulk"] != true {
				t.Errorf("user %d: bulk row not flagged: %#v", id, e.Details)
			}
			if e.ActorKind != model.ActorAdmin {
				t.Errorf("user %d: actor = %q, want admin", id, e.ActorKind)
			}
		}
	}
}

// A bulk delete must still name each user in its row, since the rows outlive them.
func TestAuditBulkDeleteKeepsNames(t *testing.T) {
	m := bulkTestManager(t)
	a := mkUser(t, m, "a", 0)
	if _, err := m.BulkUserAction(adminCtx(), []int64{a}, "delete", 0); err != nil {
		t.Fatalf("bulk delete: %v", err)
	}
	events := trail(t, m, a)
	if len(events) == 0 || events[0].Action != model.EventUserDeleted || events[0].UserName != "a" {
		t.Fatalf("want a named delete row, got %+v", events)
	}
}

// Toggling a user to the state they're already in changed nothing, so it must not
// file a row claiming it did (a double-clicked button, a stale UI).
func TestAuditNoRowForNoOpToggle(t *testing.T) {
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "Вася", 0, 0) // created enabled

	if err := m.SetUserEnabled(ctx, u.ID, true); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if hasAction(trail(t, m, u.ID), model.EventUserEnabled) {
		t.Error("a no-op enable filed an audit row")
	}
	if err := m.SetUserEnabled(ctx, u.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if !hasAction(trail(t, m, u.ID), model.EventUserDisabled) {
		t.Error("a real disable filed no audit row")
	}
	// The bulk path counts MATCHED rows, so it must filter on the prior state too.
	if _, err := m.BulkUserAction(ctx, []int64{u.ID}, "disable", 0); err != nil {
		t.Fatalf("bulk disable: %v", err)
	}
	var disables int
	for _, e := range trail(t, m, u.ID) {
		if e.Action == model.EventUserDisabled {
			disables++
		}
	}
	if disables != 1 {
		t.Errorf("got %d disable rows, want 1 (the bulk no-op must not add another)", disables)
	}
}

// A bulk extend must carry the limits it did NOT touch: the row renders as a full
// "limits changed" statement, and omitting the quota made it read "без лимита".
func TestAuditBulkExtendKeepsLimits(t *testing.T) {
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "Вася", 0, time.Now().Add(24*time.Hour).Unix())
	if err := m.SetUserLimits(ctx, u.ID, 50<<30, u.ExpireAt, 3); err != nil {
		t.Fatalf("limits: %v", err)
	}
	if _, err := m.BulkUserAction(ctx, []int64{u.ID}, "extend", 30); err != nil {
		t.Fatalf("bulk extend: %v", err)
	}
	events := trail(t, m, u.ID)
	d, ok := events[0].Details.(map[string]any)
	if !ok || d["bulk"] != true {
		t.Fatalf("newest row is not the bulk extend: %+v", events[0])
	}
	if d["data_limit"] != float64(50<<30) {
		t.Errorf("extend row lost the quota: data_limit = %#v, want %d", d["data_limit"], 50<<30)
	}
	if d["device_limit"] != float64(3) {
		t.Errorf("extend row lost the device cap: %#v", d["device_limit"])
	}
}

// Self-registration is ONE action: it must not also file the generic "user created"
// row (it did when billing was off, and didn't when a trial plan was configured).
func TestAuditSelfRegistrationIsOneRow(t *testing.T) {
	m := bulkTestManager(t)
	ctx := actor.With(context.Background(), actor.UserSelf("@vasya"))
	u, err := m.CreateRegisteredUser(ctx, "Вася") // billing off ⇒ the plain fallback
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	events := trail(t, m, u.ID)
	if len(events) != 1 || events[0].Action != model.EventUserRegistered {
		t.Fatalf("want exactly one user.registered row, got %v", actions(events))
	}
	if events[0].ActorKind != model.ActorUser {
		t.Errorf("actor = %q, want user", events[0].ActorKind)
	}
}

// The page limit is clamped, and the clamp is what the HTTP layer's "was the page
// full?" cursor check relies on.
func TestEventPageLimit(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 50}, {-5, 50}, {10, 10}, {eventPageMax, eventPageMax}, {eventPageMax + 1, eventPageMax},
	}
	for _, c := range cases {
		if got := EventPageLimit(c.in); got != c.want {
			t.Errorf("EventPageLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
