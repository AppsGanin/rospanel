package core

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
)

func accessTestManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "acc.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Manager{
		store:      st,
		accLast:    make(map[string]int64),
		accPending: make(map[accPendingKey]store.ConnectionHit),
	}, st
}

// TestRecordAccessDoesNoIO pins the hot path. RecordAccess runs for every line the
// Xray access log emits; it must only touch memory, leaving the write to the
// flusher. Before this, each admitted sighting did two statements plus a full
// WorkingUsers query — the panel's single busiest write source.
func TestRecordAccessDoesNoIO(t *testing.T) {
	m, st := accessTestManager(t)
	u, err := st.CreateUser("u1", "uuid-1", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	email := fmt.Sprintf("u%d", u.ID)

	for range 50 {
		m.RecordAccess(email, "1.1.1.1", "example.com")
		m.RecordAccess(email, "2.2.2.2", "example.com")
	}

	conns, err := st.RecentConnections(u.ID, 10)
	if err != nil {
		t.Fatalf("connections: %v", err)
	}
	if len(conns) != 0 {
		t.Fatalf("RecordAccess wrote %d rows before the flush — the hot path is still doing I/O", len(conns))
	}
	// The throttle collapses the burst: two IPs, one buffered sighting each.
	if got := len(m.accPending); got != 2 {
		t.Fatalf("buffered %d sightings from 100 calls across 2 IPs, want 2", got)
	}
}

// TestFlushAccessWritesBatch: the buffered sightings land, and flushing an empty
// buffer is free.
func TestFlushAccessWritesBatch(t *testing.T) {
	m, st := accessTestManager(t)
	u, err := st.CreateUser("u1", "uuid-1", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	email := fmt.Sprintf("u%d", u.ID)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		m.RecordAccess(email, ip, "example.com")
	}

	m.FlushAccess()
	conns, err := st.RecentConnections(u.ID, 10)
	if err != nil {
		t.Fatalf("connections: %v", err)
	}
	if len(conns) != 3 {
		t.Fatalf("flushed %d connection rows, want 3", len(conns))
	}
	cur, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if cur.LastSeen == 0 {
		t.Error("flush did not stamp last_seen")
	}

	// Buffer is drained, so a second flush writes nothing new.
	if len(m.accPending) != 0 {
		t.Fatalf("buffer still holds %d sightings after a flush", len(m.accPending))
	}
	m.FlushAccess()
	conns, _ = st.RecentConnections(u.ID, 10)
	if len(conns) != 3 {
		t.Fatalf("second flush changed the row count to %d", len(conns))
	}
}

// TestRecordAccessIgnoresJunk: the access log is parsed text, so non-user emails
// must not create buffer entries.
func TestRecordAccessIgnoresJunk(t *testing.T) {
	m, _ := accessTestManager(t)
	for _, email := range []string{"", "admin", "unotanumber", "12", "u"} {
		m.RecordAccess(email, "1.1.1.1", "example.com")
	}
	if len(m.accPending) != 0 {
		t.Fatalf("buffered %d sightings from junk emails", len(m.accPending))
	}
}

// TestRecordAccessDestinationBypassesThrottle: the destination is handed to abuse
// matching BEFORE the 10s connections throttle, because a browsing user opens many
// hosts inside one window and inheriting that throttle would check the first and
// silently drop the rest. Here the abuse matcher is nil, so we assert the property
// via its precondition: the destination path runs without disturbing the throttled
// connections buffer.
func TestRecordAccessDestinationBypassesThrottle(t *testing.T) {
	m, st := accessTestManager(t)
	u, err := st.CreateUser("u1", "uuid-1", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	email := fmt.Sprintf("u%d", u.ID)
	for _, h := range []string{"a.com", "b.com", "c.com", "d.com", "e.com"} {
		m.RecordAccess(email, "1.1.1.1", h)
	}
	// The throttle still collapses one user+IP to a single buffered sighting.
	if got := len(m.accPending); got != 1 {
		t.Fatalf("buffered %d sightings for one user+IP, want 1", got)
	}
}

// TestRecordAccessWithoutDestination: rejected and unparsable lines carry no host,
// and must still produce a device sighting.
func TestRecordAccessWithoutDestination(t *testing.T) {
	m, st := accessTestManager(t)
	u, err := st.CreateUser("u1", "uuid-1", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	m.RecordAccess(fmt.Sprintf("u%d", u.ID), "1.1.1.1", "")
	if len(m.accPending) != 1 {
		t.Fatal("a line without a destination cost us the device sighting")
	}
}

// A device that stays connected must stay counted. The throttle decides how old its
// last_seen can get: up to accThrottle after the last recorded sighting, plus the gap
// to the next node sync that carries one, plus the flush. All of that has to fit in the
// online window, or a connected device drops out of the count and the online list
// between two writes. And the master's tunnel poll, once a minute, must find its pair
// unthrottled on every poll.
func TestAccessThrottleKeepsConnectedDevicesOnline(t *testing.T) {
	const flush, tunnelPoll = 5, 60
	worstSyncGap := int64(nodeapi.HoldSec + nodeapi.HoldJitter)
	if age := accThrottle + worstSyncGap + flush; age >= model.DeviceOnlineWindow {
		t.Fatalf("a connected device's last_seen can reach %ds, past the %ds online window", age, model.DeviceOnlineWindow)
	}
	if accThrottle > tunnelPoll {
		t.Fatalf("a %ds throttle skips tunnel polls %ds apart", accThrottle, tunnelPoll)
	}
}

// The flush re-checks device limits at most every deviceCheckEvery: the first flush
// checks, one straight after it does not, and one after the interval does again. The
// sightings themselves are written by every flush.
func TestFlushChecksDeviceLimitsAtMostEveryInterval(t *testing.T) {
	m, st := accessTestManager(t)
	mk := func(name string) *model.User {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	over := func(u *model.User) bool {
		t.Helper()
		got, err := st.GetUser(u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.DeviceOverSince != 0
	}
	a, b := mk("a"), mk("b")
	m.RecordAccess(model.UserEmail(a.ID), "198.51.100.1", "")
	m.RecordAccess(model.UserEmail(a.ID), "198.51.100.2", "")
	m.FlushAccess()
	if !over(a) {
		t.Fatal("the first flush did not stamp a user over their limit")
	}

	m.RecordAccess(model.UserEmail(b.ID), "198.51.100.3", "")
	m.RecordAccess(model.UserEmail(b.ID), "198.51.100.4", "")
	m.FlushAccess()
	if over(b) {
		t.Fatal("a flush straight after a check checked again")
	}
	if conns, _ := st.RecentConnections(b.ID, 10); len(conns) != 2 {
		t.Fatalf("the flush between checks wrote %d of b's 2 sightings", len(conns))
	}

	m.deviceCheckedAt.Add(-deviceCheckEvery) // the interval has passed since that check
	m.RecordAccess(model.UserEmail(b.ID), "198.51.100.5", "")
	m.FlushAccess()
	if !over(b) {
		t.Fatal("a flush after the interval did not check")
	}
}
