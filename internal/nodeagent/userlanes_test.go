package nodeagent

import (
	"testing"
	"time"
)

// A minute counts once per user and lane; what one sync cannot carry waits for the
// next instead of being dropped.
func TestUserLanesMinutesAndTake(t *testing.T) {
	t.Parallel()
	l := newUserLanes()
	at := time.Unix(1_800_000_000, 0)
	for range 10 {
		l.note(1, "vless-in", at)
	}
	l.note(1, "vless-in", at.Add(time.Minute))
	l.note(2, "vless-in", at)
	l.note(3, "awg", at)
	l.note(0, "awg", at) // not a user
	l.note(4, "", at)    // no lane

	first := l.take(2)
	rest := l.take(10)
	if len(first) != 2 || len(rest) != 1 || l.take(10) != nil {
		t.Fatalf("take: %v then %v", first, rest)
	}
	got := map[int64]int64{}
	for _, r := range append(first, rest...) {
		got[r.UserID] += r.Minutes
	}
	if got[1] != 2 || got[2] != 1 || got[3] != 1 {
		t.Errorf("minutes: %v", got)
	}
	var nilLanes *userLanes
	nilLanes.note(1, "x", at)
	if nilLanes.take(1) != nil {
		t.Error("nil buffer handed rows")
	}
}
