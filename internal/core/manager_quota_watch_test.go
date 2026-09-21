package core

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// fakeCounters stands in for the local Xray's StatsService: what TickStats reads, and
// how many times it read it.
type fakeCounters struct {
	mu    sync.Mutex
	stats map[string]xray.Traffic
	reads int
}

func (f *fakeCounters) set(id int64, up, down int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats[fmt.Sprintf("u%d", id)] = xray.Traffic{Up: up, Down: down}
}

func (f *fakeCounters) read() (map[string]xray.Traffic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	out := make(map[string]xray.Traffic, len(f.stats))
	for k, v := range f.stats {
		out[k] = v
	}
	return out, nil
}

func (f *fakeCounters) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func quotaWatchManager(t *testing.T) (*Manager, *fakeCounters) {
	t.Helper()
	m := bulkTestManager(t)
	m.reconcileCh = make(chan struct{}, 1)
	f := &fakeCounters{stats: map[string]xray.Traffic{}}
	m.statsSource = f.read
	return m, f
}

func usedOf(t *testing.T, m *Manager, id int64) (int64, string) {
	t.Helper()
	u, err := m.store.GetUser(id)
	if err != nil {
		t.Fatal(err)
	}
	return u.UsedUp + u.UsedDown, u.Status
}

// TestTickStatsFlushesWhenAQuotaRunsOut: between two minute-long flushes the ticks
// only read the counters; the tick that sees a user's quota used up writes the
// traffic at once, so the enforcement pass takes them out of the config without
// waiting out the rest of the minute.
func TestTickStatsFlushesWhenAQuotaRunsOut(t *testing.T) {
	t.Parallel()
	m, f := quotaWatchManager(t)
	capped, err := m.store.CreateUser("capped", "uuid-capped", "pw", "tok-capped", 1000, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.set(capped.ID, 0, 0)
	if err := m.TickStats(); err != nil { // the first tick is a flush: nothing flushed yet
		t.Fatal(err)
	}

	f.set(capped.ID, 300, 200)
	if err := m.TickStats(); err != nil {
		t.Fatal(err)
	}
	if used, _ := usedOf(t, m, capped.ID); used != 0 {
		t.Fatalf("a tick with quota left wrote traffic: used=%d", used)
	}

	f.set(capped.ID, 600, 500)
	if err := m.TickStats(); err != nil {
		t.Fatal(err)
	}
	used, status := usedOf(t, m, capped.ID)
	if used != 1100 {
		t.Fatalf("the tick that saw the quota run out did not flush: used=%d, want 1100", used)
	}
	if status != model.StatusLimited {
		t.Fatalf("status after the flush = %q, want %q", status, model.StatusLimited)
	}
	select {
	case <-m.reconcileCh:
	default:
		t.Fatal("the user over their quota was not synced out of the config")
	}
}

// TestTickStatsReadsNothingWithoutQuotas: with nobody on a quota, a tick between two
// flushes has nothing to watch and must not fork the Xray CLI for it.
func TestTickStatsReadsNothingWithoutQuotas(t *testing.T) {
	t.Parallel()
	m, f := quotaWatchManager(t)
	id := mkUser(t, m, "free", 0)
	f.set(id, 10, 10)
	if err := m.TickStats(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := m.TickStats(); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.readCount(); n != 1 {
		t.Fatalf("counters read %d times, want 1 (the flush)", n)
	}
	m.quota.at = time.Now().Add(-StatsFlushEvery)
	if err := m.TickStats(); err != nil {
		t.Fatal(err)
	}
	if n := f.readCount(); n != 2 {
		t.Fatalf("a due flush did not read the counters: %d reads", n)
	}
}

// TestTickStatsWatchesAQuotaSetBetweenFlushes: a quota given to a user who is already
// downloading is watched from the next tick, against the traffic flushed so far — not
// from the next flush, by which time a fast download is far past it.
func TestTickStatsWatchesAQuotaSetBetweenFlushes(t *testing.T) {
	t.Parallel()
	m, f := quotaWatchManager(t)
	id := mkUser(t, m, "late", 0)
	f.set(id, 100, 100)
	if err := m.TickStats(); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetUserLimits(id, 1000, 0, 0); err != nil {
		t.Fatal(err)
	}
	m.TriggerUserSync()
	<-m.reconcileCh

	f.set(id, 500, 400) // 700 since the flush, 900 in all: under the quota
	if err := m.TickStats(); err != nil {
		t.Fatal(err)
	}
	if used, _ := usedOf(t, m, id); used != 200 {
		t.Fatalf("a tick under the new quota flushed: used=%d, want 200", used)
	}
	f.set(id, 600, 500) // 1100 in all
	if err := m.TickStats(); err != nil {
		t.Fatal(err)
	}
	if used, status := usedOf(t, m, id); used != 1100 || status != model.StatusLimited {
		t.Fatalf("after crossing the new quota: used=%d status=%q, want 1100 %q", used, status, model.StatusLimited)
	}
}

// TestTickStatsTriesAFailedFlushOncePerInterval: with Xray down, the flush fails once
// and is not tried again on every tick — six errors a minute in the log, six forks of
// the CLI, for one outage.
func TestTickStatsTriesAFailedFlushOncePerInterval(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	reads := 0
	m.statsSource = func() (map[string]xray.Traffic, error) {
		reads++
		return nil, errors.New("xray is down")
	}
	if err := m.TickStats(); err == nil {
		t.Fatal("a failed flush reported no error")
	}
	for range 3 {
		if err := m.TickStats(); err != nil {
			t.Fatalf("a tick inside the interval reported %v", err)
		}
	}
	if reads != 1 {
		t.Fatalf("counters read %d times inside one interval, want 1", reads)
	}
}

// A counter below what it was at the last flush means Xray restarted and began again
// from zero: what it shows now is all used since, never a negative.
func TestQuotaWatchAcrossAnXrayRestart(t *testing.T) {
	t.Parallel()
	w := quotaWatch{users: map[string]quotaLeft{"u1": {up: 5000, down: 5000, left: 1000}}}
	if w.crossed(map[string]xray.Traffic{"u1": {Up: 300, Down: 400}}) {
		t.Fatal("700 bytes since a restart crossed a quota with 1000 left")
	}
	if !w.crossed(map[string]xray.Traffic{"u1": {Up: 600, Down: 500}}) {
		t.Fatal("1100 bytes since a restart did not cross a quota with 1000 left")
	}
}
