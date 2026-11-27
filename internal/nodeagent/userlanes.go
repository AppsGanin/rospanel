package nodeagent

import (
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// userLanes counts the minutes each user is active on each lane of this node, for the
// panel's per-user protocol statistics: a minute counts once however many connections
// it opened. Drained into each sync, at most userLanesPerSync rows at a time; what
// does not fit waits for the next sync rather than being dropped, since the counts
// only add up. A sync that fails after the drain loses that batch, as the connection
// samples and destinations do: minutes are an approximation, not a ledger.
type userLanes struct {
	mu      sync.Mutex
	last    map[userLaneKey]int64 // minute (unix/60) each pair was last counted in
	swept   int64
	minutes map[userLaneKey]int64
}

type userLaneKey struct {
	userID int64
	lane   string
}

const (
	// userLanesPerSync bounds the rows one sync carries (~30 bytes of JSON each).
	userLanesPerSync = 4000
	// userLanesMax bounds the buffer for a panel that stays unreachable.
	userLanesMax = 1 << 16
)

func newUserLanes() *userLanes {
	return &userLanes{last: map[userLaneKey]int64{}, minutes: map[userLaneKey]int64{}}
}

// note counts the minute at falls in for a user on a lane.
func (l *userLanes) note(userID int64, lane string, at time.Time) {
	if l == nil || lane == "" || userID <= 0 {
		return
	}
	minute := at.Unix() / 60
	k := userLaneKey{userID: userID, lane: lane}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last[k] == minute {
		return
	}
	if _, pending := l.minutes[k]; !pending && len(l.minutes) >= userLanesMax {
		return
	}
	l.last[k] = minute
	l.minutes[k]++
	if len(l.last) > 4096 && minute > l.swept {
		l.swept = minute
		for key, at := range l.last {
			if at < minute-1 {
				delete(l.last, key)
			}
		}
	}
}

// take hands over at most max rows, removing them from the buffer.
func (l *userLanes) take(max int) []nodeapi.UserLaneSample {
	if l == nil { // an Agent built bare in a test
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.minutes) == 0 {
		return nil
	}
	out := make([]nodeapi.UserLaneSample, 0, min(len(l.minutes), max))
	for k, n := range l.minutes {
		if len(out) == max {
			break
		}
		out = append(out, nodeapi.UserLaneSample{UserID: k.userID, Lane: k.lane, Minutes: n})
		delete(l.minutes, k)
	}
	return out
}
