package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// recordLocalLanes books what each lane of the panel's own server carried since the
// last read. A failed read leaves Xray's counters where they are, and the next read
// takes them; a failed write keeps what the read took (and reset) for the next one. The
// per-user accounting does not depend on it. statsMu is held.
func (m *Manager) recordLocalLanes(day string) {
	if m.sup == nil || !m.sup.Running() {
		return
	}
	lanes, err := m.sup.QueryLanesLive(m.sup.APIAddr())
	if err != nil {
		return
	}
	m.bookLocalLanes(day, lanes)
}

// bookLocalLanes writes lanes, plus whatever an earlier write could not. statsMu is held.
func (m *Manager) bookLocalLanes(day string, lanes map[string]xray.Traffic) {
	for lane, t := range m.lanesUnsaved {
		cur := lanes[lane]
		lanes[lane] = xray.Traffic{Up: cur.Up + t.Up, Down: cur.Down + t.Down}
	}
	deltas := make([]store.LaneDelta, 0, len(lanes))
	for lane, t := range lanes {
		deltas = append(deltas, store.LaneDelta{NodeID: model.LocalNodeID, Lane: lane, Day: day, Up: t.Up, Down: t.Down})
	}
	if err := m.store.ApplyLaneTraffic(deltas); err != nil {
		logErr("stats: lane traffic failed", "err", err)
		m.lanesUnsaved = lanes
		return
	}
	m.lanesUnsaved = nil
}

// LaneTraffic is one lane of one server over a period, named for the screen.
type LaneTraffic struct {
	NodeID   int64  `json:"node_id"`
	NodeName string `json:"node_name"`
	Lane     string `json:"lane"`
	Name     string `json:"name"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
}

// LaneTrafficBreakdown is what each lane of each server carried between from and to,
// busiest first.
func (m *Manager) LaneTrafficBreakdown(from, to string) ([]LaneTraffic, error) {
	totals, err := m.store.LaneTrafficTotals(from, to)
	if err != nil {
		return nil, err
	}
	nodes, _ := m.store.NodeNames() // best-effort: a failure falls back to ids
	if nodes == nil {
		nodes = map[int64]string{}
	}
	nodes[model.LocalNodeID] = model.LocalNodeName
	inbounds := map[int64]string{}
	if all, err := m.store.AllInbounds(); err == nil {
		for _, list := range all {
			for _, in := range list {
				inbounds[in.ID] = in.Name
			}
		}
	}
	out := make([]LaneTraffic, 0, len(totals))
	for _, t := range totals {
		if t.Up == 0 && t.Down == 0 {
			continue
		}
		node := nodes[t.NodeID]
		if node == "" {
			node = fmt.Sprintf("server #%d", t.NodeID)
		}
		out = append(out, LaneTraffic{NodeID: t.NodeID, NodeName: node, Lane: t.Lane,
			Name: laneName(t.Lane, inbounds), Up: t.Up, Down: t.Down})
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := out[i].Up+out[i].Down, out[j].Up+out[j].Down
		if li != lj {
			return li > lj
		}
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].Lane < out[j].Lane
	})
	return out, nil
}

// laneName is a lane's protocol name: the built-in lanes by their protocol, a custom
// inbound by the name the operator gave it.
func laneName(lane string, inbounds map[int64]string) string {
	switch lane {
	case xray.TagVLESS:
		return model.ProtoVLESS
	case xray.TagReality:
		return model.ProtoReality
	case xray.TagHysteria:
		return model.ProtoHysteria
	case xray.TagNaive:
		return model.ProtoNaive
	case xray.LaneAWG:
		return model.ProtoAWG
	}
	if id, err := strconv.ParseInt(strings.TrimPrefix(lane, "custom-"), 10, 64); err == nil {
		if name := inbounds[id]; name != "" {
			return name
		}
	}
	return lane
}

// laneReportMax bounds one lane's bytes in one node report: far beyond a minute of any
// real link, and far enough below int64 that no day's sum of reports can overflow it.
const laneReportMax = int64(1) << 42 // 4 TiB

// nodeLanes keeps the lanes of a node's report that can be its own: the built-in lanes,
// the tunnel and the custom inbounds that server has — at most once each, within
// laneReportMax. A node's report is untrusted input, and without this a compromised node
// could fill the table with made-up lanes or overflow the sums the statistics read.
// An error (the node's inbounds unreadable) is the report's to fail: it is then not
// acked, and comes back.
func (m *Manager) nodeLanes(nodeID int64, day string, in []nodeapi.LaneDelta) ([]store.LaneDelta, error) {
	if len(in) == 0 {
		return nil, nil
	}
	list, err := m.store.Inbounds(nodeID)
	if err != nil {
		return nil, err
	}
	custom := make(map[string]bool, len(list))
	for i := range list {
		custom[list[i].Tag()] = true
	}
	seen := map[string]bool{}
	out := make([]store.LaneDelta, 0, len(in))
	for _, l := range in {
		builtin := l.Lane == xray.LaneAWG || (xray.IsLaneTag(l.Lane) && !strings.HasPrefix(l.Lane, "custom-"))
		if seen[l.Lane] || (!builtin && !custom[l.Lane]) {
			continue
		}
		seen[l.Lane] = true
		out = append(out, store.LaneDelta{NodeID: nodeID, Lane: l.Lane, Day: day,
			Up: min(nonNeg(l.Up), laneReportMax), Down: min(nonNeg(l.Down), laneReportMax)})
	}
	return out, nil
}
