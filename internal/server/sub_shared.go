package server

import (
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// subShared is what every subscription is built from that is the same for every user:
// the nodes' link settings, the servers' custom inbounds and the external servers.
//
// Each fetch read all three from the store, the node list being a read of every node's
// settings, and those reads queue behind the one SQLite connection the whole panel
// shares. In the load test a burst of sixty fetches a second got through at thirty-seven
// while the CPU sat at 41%.
//
// One read now serves for subSharedTTL, and any request that may change something
// through the panel or the API makes the next one fresh (see notingWrites), so an
// operator's own edit reaches the next subscription. What moves on its own — a node's
// reported certificate or last sight, an external subscription's refresh — reaches
// subscriptions within the TTL; their clients refresh in hours.
type subShared struct {
	mu     sync.Mutex
	writes uint64 // the write count the read was taken under
	at     time.Time
	read   *subSharedRead // nil until a read succeeds whole
}

// subSharedRead is one read. Nothing in it is handed out: callers get copies.
type subSharedRead struct {
	nodes    []*model.Settings
	inbounds map[int64][]model.Inbound
	ext      []model.ExtServer
}

const subSharedTTL = 5 * time.Second

// sharedSubInputs returns the shared inputs, read afresh when the last read is older than
// subSharedTTL or predates a change. A read that fails in part serves its own request
// degraded, the way each read always did — no nodes, no custom inbounds or no external
// servers — and is not kept, so the next request tries again.
func (rt *Router) sharedSubInputs() subSharedRead {
	// Taken before reading, so a change landing during the read leaves it looking older
	// than it is.
	writes := rt.writes.Load()
	c := &rt.subShared
	c.mu.Lock()
	if c.read != nil && c.writes == writes && time.Since(c.at) < subSharedTTL {
		out := c.read.clone()
		c.mu.Unlock()
		return out
	}
	c.mu.Unlock()

	at := time.Now()
	read := &subSharedRead{}
	whole := true
	var err error
	if read.nodes, err = rt.mgr.NodeLinkSettings(); err != nil {
		read.nodes, whole = nil, false
	}
	if read.inbounds, err = rt.mgr.Store().AllInbounds(); err != nil {
		read.inbounds, whole = nil, false
	}
	if read.ext, err = rt.mgr.Store().EnabledExtServers(); err != nil {
		slog.Error("extsub: reading the enabled servers failed", "err", err)
		read.ext, whole = nil, false
	}
	if whole {
		c.mu.Lock()
		c.read, c.writes, c.at = read, writes, at
		c.mu.Unlock()
	}
	return read.clone()
}

func (r *subSharedRead) clone() subSharedRead {
	out := subSharedRead{ext: slices.Clone(r.ext)}
	if r.nodes != nil {
		out.nodes = make([]*model.Settings, len(r.nodes))
		for i, n := range r.nodes {
			out.nodes[i] = n.Clone()
		}
	}
	if r.inbounds != nil {
		out.inbounds = maps.Clone(r.inbounds)
		for id, list := range out.inbounds {
			copied := make([]model.Inbound, len(list))
			for i := range list {
				copied[i] = list[i].Clone()
			}
			out.inbounds[id] = copied
		}
	}
	return out
}
