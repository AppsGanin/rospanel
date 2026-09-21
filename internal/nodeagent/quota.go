package nodeagent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// The node samples its traffic about once a minute, and the panel counts it against
// users' quotas only when a report arrives: a user downloading through a node went on
// for up to two minutes past their quota before the panel knew. Sampling more often
// would make every sync carry traffic and every sync a database write on the panel.
//
// Instead the panel answers each sync with what the users active here have left, and
// every quotaTickEvery the agent reads its Xray's counters and adds up what the panel
// has not seen yet — traffic not sampled, sampled but not sent, sent but not
// acknowledged. A user past what they had left gets a sample and a report straight
// away, marked so the panel enforces it at once; the connections they hold are closed
// when the panel's removal arrives. A user who starts using the node cuts the poll's
// wait, so what they have left arrives within a tick rather than a hold.

const (
	quotaTickEvery = 10 * time.Second
	// quotaUsersMax bounds how many users one request asks the quota of.
	quotaUsersMax = 20000
	// quotaActiveFor is how long a user whose traffic moved stays asked about. Every
	// request names them, so every answer — the held ones too — says what they have left.
	quotaActiveFor = 2 * time.Minute
)

func (a *Agent) quotaLoop(ctx context.Context) {
	t := time.NewTicker(quotaTickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.checkQuota()
		}
	}
}

// settleReport acknowledges the report the panel counted and takes what its answer
// says the watched users have left, in one step: between the two, the acknowledged
// traffic would count neither as unseen nor in the figures being watched. An answer
// without a quota — nobody asked about, or a panel too old to know — watches nobody.
func (a *Agent) settleReport(ack int64, left map[int64]int64) {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	a.ackReportLocked(ack)
	a.quotaLeft = left
}

// forgetQuotaAsked makes the next request ask as if anew: one that went unanswered
// told the node nothing, so the users it named are not watched yet.
func (a *Agent) forgetQuotaAsked() {
	a.statsMu.Lock()
	a.quotaAsked = nil
	a.statsMu.Unlock()
}

// takeQuotaUsers is who the next request asks the quota of — the users whose traffic
// moved lately, or who have traffic the panel has not acknowledged — whether it
// carries a user who ran out, and whether it names anyone the last request did not.
// settled is whether the request carries all the traffic sampled so far, or there is
// none left to send. Caller holds statsMu.
func (a *Agent) takeQuotaUsers(settled bool) ([]int64, bool, bool) {
	now := time.Now()
	seen := make(map[int64]struct{}, len(a.quotaActive)+len(a.pending)+len(a.inflight))
	for id, at := range a.quotaActive {
		if now.Sub(at) >= quotaActiveFor {
			delete(a.quotaActive, id)
			continue
		}
		seen[id] = struct{}{}
	}
	for id := range a.pending {
		seen[id] = struct{}{}
	}
	for id := range a.inflight {
		seen[id] = struct{}{}
	}
	users := make([]int64, 0, len(seen))
	asked := make(map[int64]struct{}, len(seen))
	fresh := false
	for id := range seen {
		if len(users) == quotaUsersMax {
			break
		}
		users = append(users, id)
		asked[id] = struct{}{}
		if _, ok := a.quotaAsked[id]; !ok {
			fresh = true
		}
	}
	a.quotaAsked = asked
	crossed := a.quotaCrossed
	// The mark stays on until the traffic that crossed goes out. A request resending an
	// unacknowledged report carries an older sample, and the panel would enforce against
	// figures without the crossing in them.
	if settled {
		a.quotaCrossed = false
	}
	return users, crossed, fresh
}

// checkQuota is one tick of the watch.
func (a *Agent) checkQuota() {
	if !a.sup.Running() {
		return
	}
	stats, err := a.sup.QueryStatsLive(a.sup.APIAddr())
	if err != nil {
		return
	}
	a.statsMu.Lock()
	crossed, newcomer := a.quotaCheckLocked(stats)
	a.statsMu.Unlock()
	switch {
	case crossed:
		slog.Info("node: a user ran out of quota between traffic samples, reporting now")
		a.sampleStats()   // what crossed goes into the report
		a.interruptSync() // and the report goes now
	case newcomer:
		// The request naming them goes now, not when the panel lets the held one go.
		a.interruptWait()
	}
}

// quotaCheckLocked marks who is active and reports whether anyone watched has used up,
// in traffic the panel has not seen, what they had left, and whether someone active
// is not asked about yet. A user who ran out stops being watched until the panel's
// next answer. Caller holds statsMu.
func (a *Agent) quotaCheckLocked(stats map[string]xray.Traffic) (crossed, newcomer bool) {
	newcomer = a.markActiveLocked(stats)
	for uid, left := range a.quotaLeft {
		email := fmt.Sprintf("u%d", uid)
		up, down := counterDelta(stats[email], a.lastCounters[email])
		unseen := nonNeg(up) + nonNeg(down) + deltaBytes(a.pending[uid]) + deltaBytes(a.inflight[uid])
		if unseen >= left {
			crossed = true
			delete(a.quotaLeft, uid)
		}
	}
	if crossed {
		a.quotaCrossed = true
	}
	return crossed, newcomer
}

// markActiveLocked notes the users whose counters moved since the last sample as
// active now, and reports whether one of them is not asked about yet. Caller holds
// statsMu.
func (a *Agent) markActiveLocked(stats map[string]xray.Traffic) bool {
	if a.quotaActive == nil {
		a.quotaActive = map[int64]time.Time{}
	}
	now := time.Now()
	newcomer := false
	for email, cur := range stats {
		uid, ok := userIDFromEmail(email)
		if !ok {
			continue
		}
		if up, down := counterDelta(cur, a.lastCounters[email]); up > 0 || down > 0 {
			a.quotaActive[uid] = now
			if _, asked := a.quotaAsked[uid]; !asked {
				newcomer = true
			}
		}
	}
	return newcomer
}

// counterDelta is how far a counter moved since prev; one below prev means Xray
// restarted and began again from zero.
func counterDelta(cur, prev xray.Traffic) (up, down int64) {
	up, down = cur.Up-prev.Up, cur.Down-prev.Down
	if cur.Up < prev.Up {
		up = cur.Up
	}
	if cur.Down < prev.Down {
		down = cur.Down
	}
	return up, down
}

func deltaBytes(d *nodeapi.TrafficDelta) int64 {
	if d == nil {
		return 0
	}
	return nonNeg(d.Up) + nonNeg(d.Down)
}

func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
