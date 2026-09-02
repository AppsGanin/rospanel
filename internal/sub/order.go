package sub

import (
	"sort"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Order arranges the servers a subscription spans for one client, and drops the
// ones that have no room, according to the operator's mode:
//
//   - manual: weight (higher first), then the list order (master, then nodes).
//   - nearest: servers in the client's country first, then the rest; manual
//     order inside each half.
//   - load: least loaded first — the load being online users over capacity, or
//     the bare online count where no capacity is stated.
//   - nearest_load: the client's country first, least loaded within.
//
// A server marked hide-when-full with a capacity it has reached is left out —
// unless that would leave nothing, in which case everything stays: an empty
// subscription strands every client, while a full server merely slows them.
//
// clientCC is the client's country (blank when unknown, in which case nothing is
// "nearest" and the mode degrades to its second key). online is each server's
// current online users by server id; a missing entry reads as zero.
func Order(servers []Server, mode, clientCC string, online map[int64]int) []Server {
	mode = model.OrderModeOr(mode)
	type ranked struct {
		s     Server
		idx   int
		near  int     // 0 = the client's country, 1 = elsewhere
		load  float64 // online/capacity, or online/1 with no capacity
		full  bool
		weigh int
	}
	list := make([]ranked, 0, len(servers))
	kept := 0
	for i, s := range servers {
		p := s.Set.ServerPlacement
		n := online[s.Set.ServerID]
		r := ranked{s: s, idx: i, near: 1, weigh: p.Weight}
		if clientCC != "" && p.Country != "" && p.Country == clientCC {
			r.near = 0
		}
		if p.Capacity > 0 {
			r.load = float64(n) / float64(p.Capacity)
			r.full = p.HideWhenFull && n >= p.Capacity
		} else {
			// No stated capacity: the count itself, scaled so a server with a
			// capacity always compares by its ratio against one without by raw load.
			r.load = float64(n)
		}
		if !r.full {
			kept++
		}
		list = append(list, r)
	}
	// Never empty the list on account of load.
	if kept == 0 {
		for i := range list {
			list[i].full = false
		}
	}
	byNear := mode == model.OrderNearest || mode == model.OrderNearestLoad
	byLoad := mode == model.OrderLoad || mode == model.OrderNearestLoad
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if byNear && a.near != b.near {
			return a.near < b.near
		}
		if byLoad && a.load != b.load {
			return a.load < b.load
		}
		if a.weigh != b.weigh {
			return a.weigh > b.weigh
		}
		return a.idx < b.idx
	})
	out := make([]Server, 0, len(list))
	for _, r := range list {
		if !r.full {
			out = append(out, r.s)
		}
	}
	return out
}
