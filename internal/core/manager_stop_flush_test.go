package core

import (
	"testing"
	"time"
)

// A stopping panel writes what the counters hold since the last flush, without
// counting any of it twice on the next start.
func TestFlushTrafficOnStop(t *testing.T) {
	t.Parallel()
	m, f := quotaWatchManager(t)
	u, err := m.store.CreateUser("ann", "uuid-ann", "pw", "tok-ann", 100, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.set(u.ID, 0, 0)
	if err := m.PollStats(); err != nil { // the last regular flush
		t.Fatal(err)
	}
	f.set(u.ID, 400, 600) // counted since, past the 100-byte quota

	m.FlushTrafficOnStop(5 * time.Second)
	if used, _ := usedOf(t, m, u.ID); used != 1000 {
		t.Errorf("used %d after the stop flush, want 1000", used)
	}
	// The next start reads the counters from zero; the flush left the baseline at
	// what it read, so nothing is counted twice.
	if err := m.PollStats(); err != nil {
		t.Fatal(err)
	}
	if used, _ := usedOf(t, m, u.ID); used != 1000 {
		t.Errorf("a poll after the stop flush counted again: %d", used)
	}
}
