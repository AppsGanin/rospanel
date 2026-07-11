package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// add appends one event and fails the test on error.
func add(t *testing.T, s *Store, ev model.UserEvent) {
	t.Helper()
	if err := s.AddUserEvent(ev); err != nil {
		t.Fatalf("AddUserEvent: %v", err)
	}
}

func TestUserEventRoundTrip(t *testing.T) {
	s := openTestStore(t)
	add(t, s, model.UserEvent{
		UserID: 7, UserName: "Вася", Action: model.EventUserCreated,
		ActorKind: model.ActorAdmin, ActorName: "root",
		Details: map[string]any{"data_limit": 1024, "expire_at": 0},
	})

	events, err := s.ListUserEvents(7, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	e := events[0]
	if e.UserName != "Вася" || e.Action != model.EventUserCreated ||
		e.ActorKind != model.ActorAdmin || e.ActorName != "root" {
		t.Fatalf("round-trip mismatch: %+v", e)
	}
	if e.CreatedAt == 0 {
		t.Error("CreatedAt should default to now")
	}
	// Details come back as decoded JSON — numbers as float64.
	d, ok := e.Details.(map[string]any)
	if !ok {
		t.Fatalf("details did not decode to an object: %#v", e.Details)
	}
	if d["data_limit"] != float64(1024) {
		t.Errorf("details[data_limit] = %#v, want 1024", d["data_limit"])
	}
}

// A row with no details must come back with nil Details, not an empty object — the
// UI keys "is there anything to show" off exactly that.
func TestUserEventNilDetails(t *testing.T) {
	s := openTestStore(t)
	add(t, s, model.UserEvent{UserID: 1, Action: model.EventUserEnabled, ActorKind: model.ActorSystem})
	events, _ := s.ListUserEvents(1, 10, 0)
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Details != nil {
		t.Errorf("Details = %#v, want nil", events[0].Details)
	}
}

// Events are scoped to their user and returned newest-first.
func TestUserEventScopeAndOrder(t *testing.T) {
	s := openTestStore(t)
	add(t, s, model.UserEvent{UserID: 1, Action: model.EventUserCreated})
	add(t, s, model.UserEvent{UserID: 2, Action: model.EventUserCreated})
	add(t, s, model.UserEvent{UserID: 1, Action: model.EventUserDisabled})

	events, _ := s.ListUserEvents(1, 10, 0)
	if len(events) != 2 {
		t.Fatalf("want 2 events for user 1, got %d", len(events))
	}
	if events[0].Action != model.EventUserDisabled {
		t.Errorf("newest event = %q, want the disable (newest first)", events[0].Action)
	}
}

// Paging backwards with the id cursor must neither skip nor repeat a row.
func TestUserEventPaging(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 5; i++ {
		add(t, s, model.UserEvent{UserID: 1, Action: model.EventUserEnabled})
	}
	first, _ := s.ListUserEvents(1, 2, 0)
	if len(first) != 2 {
		t.Fatalf("page 1: want 2, got %d", len(first))
	}
	second, _ := s.ListUserEvents(1, 2, first[len(first)-1].ID)
	if len(second) != 2 {
		t.Fatalf("page 2: want 2, got %d", len(second))
	}
	third, _ := s.ListUserEvents(1, 2, second[len(second)-1].ID)
	if len(third) != 1 {
		t.Fatalf("page 3: want the last 1, got %d", len(third))
	}
	seen := map[int64]bool{}
	for _, e := range append(append(first, second...), third...) {
		if seen[e.ID] {
			t.Fatalf("event %d returned twice across pages", e.ID)
		}
		seen[e.ID] = true
	}
	if len(seen) != 5 {
		t.Errorf("paged over %d distinct events, want 5", len(seen))
	}
}

func TestListEventsFilters(t *testing.T) {
	s := openTestStore(t)
	add(t, s, model.UserEvent{UserID: 1, Action: model.EventUserCreated, ActorKind: model.ActorAdmin})
	add(t, s, model.UserEvent{UserID: 2, Action: model.EventUserExpired, ActorKind: model.ActorSystem})
	add(t, s, model.UserEvent{UserID: 1, Action: model.EventUserExpired, ActorKind: model.ActorSystem})

	all, _ := s.ListEvents(UserEventFilter{Limit: 10})
	if len(all) != 3 {
		t.Fatalf("unfiltered: want 3, got %d", len(all))
	}
	byAction, _ := s.ListEvents(UserEventFilter{Action: model.EventUserExpired, Limit: 10})
	if len(byAction) != 2 {
		t.Errorf("by action: want 2, got %d", len(byAction))
	}
	byActor, _ := s.ListEvents(UserEventFilter{ActorKind: model.ActorAdmin, Limit: 10})
	if len(byActor) != 1 {
		t.Errorf("by actor: want 1, got %d", len(byActor))
	}
	byUser, _ := s.ListEvents(UserEventFilter{UserID: 1, Limit: 10})
	if len(byUser) != 2 {
		t.Errorf("by user: want 2, got %d", len(byUser))
	}
	combined, _ := s.ListEvents(UserEventFilter{
		UserID: 1, Action: model.EventUserExpired, ActorKind: model.ActorSystem, Limit: 10,
	})
	if len(combined) != 1 {
		t.Errorf("combined filters: want 1, got %d", len(combined))
	}
}

// The retention sweep drops old rows and keeps recent ones.
func TestPurgeUserEvents(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().Unix()
	old := now - int64(model.UserEventRetentionDays+1)*86400
	add(t, s, model.UserEvent{UserID: 1, Action: model.EventUserCreated, CreatedAt: old})
	add(t, s, model.UserEvent{UserID: 1, Action: model.EventUserEnabled, CreatedAt: now})

	cutoff := now - int64(model.UserEventRetentionDays)*86400
	n, err := s.PurgeUserEvents(cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d rows, want 1", n)
	}
	left, _ := s.ListUserEvents(1, 10, 0)
	if len(left) != 1 || left[0].Action != model.EventUserEnabled {
		t.Errorf("purge removed the wrong row: %+v", left)
	}
}

// A deleted user's trail must survive them — that's the point of an audit log, and
// why user_events has no foreign key to users.
func TestUserEventsOutliveTheUser(t *testing.T) {
	s := openTestStore(t)
	u, err := s.CreateUser("Вася", "uuid-1", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	add(t, s, model.UserEvent{UserID: u.ID, UserName: u.Name, Action: model.EventUserCreated})
	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	add(t, s, model.UserEvent{UserID: u.ID, UserName: u.Name, Action: model.EventUserDeleted})

	events, err := s.ListUserEvents(u.ID, 10, 0)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want the trail to survive deletion (2 events), got %d", len(events))
	}
	if events[0].UserName != "Вася" {
		t.Errorf("denormalized name lost: %+v", events[0])
	}
}
