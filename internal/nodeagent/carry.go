package nodeagent

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// Traffic that has not reached the panel yet survives a stop: it is written to
// carry.json as the agent stops and sent with the first syncs after it starts.
// What was in flight keeps its report id, so a batch the panel already took before
// the stop is dropped there as a duplicate rather than counted twice.

type trafficCarry struct {
	InflightID    int64                           `json:"inflight_id,omitempty"`
	Inflight      map[int64]*nodeapi.TrafficDelta `json:"inflight,omitempty"`
	InflightLanes map[string]*nodeapi.LaneDelta   `json:"inflight_lanes,omitempty"`
	Pending       map[int64]*nodeapi.TrafficDelta `json:"pending,omitempty"`
	PendingLanes  map[string]*nodeapi.LaneDelta   `json:"pending_lanes,omitempty"`
}

func carryPath(dataDir string) string { return filepath.Join(dataDir, "carry.json") }

// keepTrafficOnStop reads Xray's counters one last time and writes everything not
// yet acknowledged by the panel to disk. Called before Xray is stopped for good. The
// read is bounded: a wedged Xray must not hold the stop, and what was sampled
// before is kept either way.
func (a *Agent) keepTrafficOnStop() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.sampleStats()
	}()
	select {
	case <-done:
	case <-time.After(carrySampleTimeout):
		slog.Warn("node: the last traffic sample did not finish in time — keeping what was sampled before")
	}
	a.statsMu.Lock()
	c := trafficCarry{
		InflightID: a.inflightID, Inflight: a.inflight, InflightLanes: a.inflightLanes,
		Pending: a.pending, PendingLanes: a.pendingLanes,
	}
	users, lanes := len(c.Inflight)+len(c.Pending), len(c.InflightLanes)+len(c.PendingLanes)
	var b []byte
	var err error
	if users+lanes > 0 {
		b, err = json.Marshal(&c)
	}
	a.statsMu.Unlock()
	if users+lanes == 0 {
		return
	}
	if err == nil {
		tmp := carryPath(a.dataDir) + ".new"
		if err = os.WriteFile(tmp, b, 0o600); err == nil {
			err = os.Rename(tmp, carryPath(a.dataDir))
		}
	}
	if err != nil {
		slog.Warn("node: could not keep the unsent traffic", "err", err)
		return
	}
	slog.Info("node: unsent traffic kept for the next start", "users", users, "lanes", lanes)
}

// carrySampleTimeout bounds the last sample on stop: Xray's API, its CLI fallback and
// the lane counters each wait up to ten seconds on a wedged process.
const carrySampleTimeout = 10 * time.Second

// restoreCarry takes back the traffic a previous run kept, before the first sync.
// The file is removed once read: it is in memory again, and a second start must
// not count it twice.
func (a *Agent) restoreCarry() {
	path := carryPath(a.dataDir)
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	// Removed before it is used: a file that stays would be restored again by every
	// start, the same traffic counted each time. One that cannot be removed is
	// dropped instead — late traffic lost beats traffic counted twice.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("node: kept traffic cannot be removed — dropping it rather than counting it twice", "err", err)
		return
	}
	var c trafficCarry
	if err := json.Unmarshal(b, &c); err != nil {
		slog.Warn("node: unreadable kept traffic dropped", "err", err)
		return
	}
	dropNil(c.Inflight)
	dropNil(c.Pending)
	dropNil(c.InflightLanes)
	dropNil(c.PendingLanes)
	a.statsMu.Lock()
	restoredID := int64(0)
	if c.InflightID != 0 && len(c.Inflight)+len(c.InflightLanes) > 0 {
		a.inflightID, a.inflight, a.inflightLanes = c.InflightID, c.Inflight, c.InflightLanes
		if a.inflight == nil {
			a.inflight = map[int64]*nodeapi.TrafficDelta{}
		}
		a.reportSeq = max(a.reportSeq, c.InflightID)
		restoredID = c.InflightID
	}
	for id, d := range c.Pending {
		if p := a.pending[id]; p != nil {
			p.Up += d.Up
			p.Down += d.Down
		} else {
			a.pending[id] = d
		}
	}
	if len(c.PendingLanes) > 0 {
		if a.pendingLanes == nil {
			a.pendingLanes = map[string]*nodeapi.LaneDelta{}
		}
		for k, d := range c.PendingLanes {
			if p := a.pendingLanes[k]; p != nil {
				p.Up += d.Up
				p.Down += d.Down
			} else {
				a.pendingLanes[k] = d
			}
		}
		if a.pendingLanesSince.IsZero() {
			a.pendingLanesSince = time.Now()
		}
	}
	a.statsMu.Unlock()
	// The id the restored batch carries is never handed out again, even if the
	// state file missed it.
	if restoredID != 0 {
		a.noteReportID(restoredID)
	}
	slog.Info("node: traffic kept by the last run restored",
		"users", len(c.Inflight)+len(c.Pending), "lanes", len(c.InflightLanes)+len(c.PendingLanes))
}

// dropNil removes entries a damaged file left empty: a nil delta would be
// dereferenced when the report is built.
func dropNil[K comparable, V any](m map[K]*V) {
	for k, v := range m {
		if v == nil {
			delete(m, k)
		}
	}
}
