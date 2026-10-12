package nodeagent

import (
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Traffic by protocol: Xray's per-inbound counters, read and zeroed each sample, and the
// AmneziaWG tunnel's share of the peers' counters, both accumulated into pendingLanes and
// reported with the users' traffic under the same report id.

// sampleLanes adds what each Xray lane carried since the last read.
func (a *Agent) sampleLanes() {
	lanes, err := a.sup.QueryLanesLive(a.sup.APIAddr())
	if err != nil || len(lanes) == 0 {
		return
	}
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	for lane, t := range lanes {
		a.addLaneLocked(lane, t.Up, t.Down)
	}
}

// lanesAlone is how long lane traffic waits for user traffic to ride out with before it
// goes in a report of its own. A node nobody uses still sees scanners at its decoy, and a
// report every sample for that would be a write a minute on the panel for nothing — and a
// report in flight that the users' first traffic would have to wait behind.
var lanesAlone = 5 * time.Minute

// lanesDueLocked reports whether pending lane traffic should go out without user
// traffic. Caller holds statsMu.
func (a *Agent) lanesDueLocked() bool {
	return len(a.pendingLanes) > 0 && time.Since(a.pendingLanesSince) >= lanesAlone
}

// addLaneLocked adds to one lane's pending traffic. Caller holds statsMu.
func (a *Agent) addLaneLocked(lane string, up, down int64) {
	if up <= 0 && down <= 0 {
		return
	}
	if len(a.pendingLanes) == 0 {
		a.pendingLanesSince = time.Now()
	}
	if a.pendingLanes == nil {
		a.pendingLanes = map[string]*nodeapi.LaneDelta{}
	}
	d := a.pendingLanes[lane]
	if d == nil {
		d = &nodeapi.LaneDelta{Lane: lane}
		a.pendingLanes[lane] = d
	}
	d.Up += max(up, 0)
	d.Down += max(down, 0)
}

// awgLane is the lane key of the AmneziaWG tunnel.
const awgLane = xray.LaneAWG
