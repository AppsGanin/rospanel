package core

import (
	"fmt"
	"testing"

	"github.com/AppsGanin/rospanel/internal/store"
)

// A full sighting buffer asks for a flush instead of waiting out the interval, and
// holds on to what arrives meanwhile: the old cap was the flush trigger's size, and
// past it sightings were dropped.
func TestAccessBufferFlushesEarlyWithoutDropping(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	m.accFlushDue = make(chan struct{}, 1)
	ip := func(i int) string { return fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255) }

	for i := 0; i < accFlushAt-1; i++ {
		m.RecordAccess("u1", ip(i), "")
	}
	select {
	case <-m.AccessFlushDue():
		t.Fatal("a flush was asked for before the buffer reached its trigger")
	default:
	}
	m.RecordAccess("u1", ip(accFlushAt-1), "")
	select {
	case <-m.AccessFlushDue():
	default:
		t.Fatal("a full buffer did not ask for a flush")
	}
	for i := accFlushAt; i < accFlushAt+500; i++ {
		m.RecordAccess("u1", ip(i), "")
	}
	if n := len(m.accPending); n != accFlushAt+500 {
		t.Fatalf("%d sightings buffered, want all %d", n, accFlushAt+500)
	}
}

// At the hard bound a new pair is turned away, but not throttled as though it had been
// kept: its next line, once there is room, is recorded.
func TestAccessSightingTurnedAwayIsNotThrottled(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	for i := 0; len(m.accPending) < accPendingMax; i++ {
		m.accPending[accPendingKey{userID: 2, ip: fmt.Sprint("filler-", i)}] = store.ConnectionHit{}
	}
	m.RecordAccess("u1", "198.51.100.1", "")
	if _, ok := m.accPending[accPendingKey{userID: 1, ip: "198.51.100.1"}]; ok {
		t.Fatal("a sighting was buffered past the bound")
	}
	clear(m.accPending)
	m.RecordAccess("u1", "198.51.100.1", "")
	if _, ok := m.accPending[accPendingKey{userID: 1, ip: "198.51.100.1"}]; !ok {
		t.Fatal("the pair turned away at the bound was throttled when there was room again")
	}
}
