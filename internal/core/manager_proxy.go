package core

import (
	"context"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/proxypool"
)

// defaultProxyRefresh is the auto-refresh cadence for the URL-sourced proxy list
// when the operator hasn't picked one (free public proxies churn fast).
const defaultProxyRefresh = 30 * time.Minute

// proxyRefreshDuration maps the configured minutes to a duration: 0 → the default
// (preserves auto-refresh for configs saved before this was selectable), a
// negative value → "never" (0 duration), otherwise that many minutes.
func proxyRefreshDuration(minutes int) time.Duration {
	switch {
	case minutes < 0:
		return 0
	case minutes == 0:
		return defaultProxyRefresh
	default:
		return time.Duration(minutes) * time.Minute
	}
}

// currentProxyRefresh reads the operator's refresh cadence from settings (0 if
// "never").
func (m *Manager) currentProxyRefresh() time.Duration {
	set, err := m.store.GetSettings()
	if err != nil {
		return defaultProxyRefresh
	}
	return proxyRefreshDuration(set.Routing.ProxyRefreshMinutes)
}

// buildProxies resolves the proxies of every enabled egress lane: its manual
// entries merged with whatever its source URLs serve (best-effort per-URL fetch —
// failures are skipped). Lanes with no usable proxies are left out of the map, so
// the generator sees them as inactive.
func (m *Manager) buildProxies(rc model.RoutingConfig) map[string][]model.ProxyEndpoint {
	out := make(map[string][]model.ProxyEndpoint, len(rc.Lanes))
	for _, lane := range rc.Lanes {
		if !lane.Enabled {
			continue
		}
		lines := append([]string{}, lane.Manual...)
		for _, url := range lane.URLs {
			if url = strings.TrimSpace(url); url == "" {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			fetched, err := proxypool.Fetch(ctx, url)
			cancel()
			if err != nil {
				logWarn("proxypool: fetch failed", "lane", lane.ID, "url", url, "err", err)
				continue
			}
			lines = append(lines, fetched...)
		}
		if eps := proxypool.Parse(lines); len(eps) > 0 {
			out[lane.ID] = eps
		}
	}
	return out
}

// seedProxiesFromManual resolves only the manual entries of each enabled lane —
// no network. Used at boot to have something in the pool instantly.
func seedProxiesFromManual(rc model.RoutingConfig) map[string][]model.ProxyEndpoint {
	out := make(map[string][]model.ProxyEndpoint, len(rc.Lanes))
	for _, lane := range rc.Lanes {
		if !lane.Enabled {
			continue
		}
		if eps := proxypool.Parse(lane.Manual); len(eps) > 0 {
			out[lane.ID] = eps
		}
	}
	return out
}

func (m *Manager) getProxies() map[string][]model.ProxyEndpoint {
	m.proxyMu.Lock()
	defer m.proxyMu.Unlock()
	out := make(map[string][]model.ProxyEndpoint, len(m.proxies))
	for id, eps := range m.proxies {
		out[id] = append([]model.ProxyEndpoint(nil), eps...)
	}
	return out
}

func (m *Manager) setProxies(p map[string][]model.ProxyEndpoint) {
	m.proxyMu.Lock()
	m.proxies = p
	m.proxyMu.Unlock()
}

// getNodeProxies returns a deep copy of one node's cached lane proxies (empty map
// if the node has none), so NodeDesiredState resolves lanes against the node's own
// pool without a network round-trip on the sync path.
func (m *Manager) getNodeProxies(nodeID int64) map[string][]model.ProxyEndpoint {
	m.proxyMu.Lock()
	defer m.proxyMu.Unlock()
	src := m.nodeProxies[nodeID]
	out := make(map[string][]model.ProxyEndpoint, len(src))
	for id, eps := range src {
		out[id] = append([]model.ProxyEndpoint(nil), eps...)
	}
	return out
}

// setNodeProxies stores a node's resolved lane proxies, dropping the entry entirely
// when empty so ProxyCount-style scans and the cache stay tidy.
func (m *Manager) setNodeProxies(nodeID int64, p map[string][]model.ProxyEndpoint) {
	m.proxyMu.Lock()
	defer m.proxyMu.Unlock()
	if len(p) == 0 {
		delete(m.nodeProxies, nodeID)
		return
	}
	if m.nodeProxies == nil {
		m.nodeProxies = map[int64]map[string][]model.ProxyEndpoint{}
	}
	m.nodeProxies[nodeID] = p
}

// resolveNodeProxies re-resolves one node's own lane proxies from its routing
// (best-effort network fetch), caching the result. A node with no enabled lanes has
// its cache cleared. Returns true if the resolved pool differs from the cache.
func (m *Manager) resolveNodeProxies(n *model.Node) bool {
	var next map[string][]model.ProxyEndpoint
	if n.Enabled && n.Routing != nil && len(n.Routing.Lanes) > 0 {
		next = m.buildProxies(*n.Routing)
	}
	if proxiesEqual(next, m.getNodeProxies(n.ID)) {
		return false
	}
	m.setNodeProxies(n.ID, next)
	return true
}

// seedNodeProxies loads every enabled node's MANUAL lane proxies synchronously at
// boot (no network), mirroring SeedProxies for the local server: nodes come up with
// their manual proxies immediately; URL-sourced ones fill in on the first refresh.
func (m *Manager) seedNodeProxies() {
	nodes, err := m.store.ListNodes()
	if err != nil {
		return
	}
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || n.Routing == nil || len(n.Routing.Lanes) == 0 {
			continue
		}
		m.setNodeProxies(n.ID, seedProxiesFromManual(*n.Routing))
	}
}

// RefreshNodeProxies re-resolves every enabled node's own lane proxies and wakes any
// node whose pool changed so it re-pulls a config with the fresh endpoints. Runs on
// the same timer as RefreshProxies. Nodes with no lanes (disabled, or lanes removed)
// have their cache dropped and are woken so their config loses the stale endpoints.
func (m *Manager) RefreshNodeProxies() {
	nodes, err := m.store.ListNodes()
	if err != nil {
		return
	}
	live := map[int64]bool{}
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || n.Routing == nil || len(n.Routing.Lanes) == 0 {
			continue
		}
		live[n.ID] = true
		if m.resolveNodeProxies(n) {
			m.nodes.wakeOne(n.ID)
		}
	}
	m.proxyMu.Lock()
	var dropped []int64
	for id := range m.nodeProxies {
		if !live[id] {
			delete(m.nodeProxies, id)
			dropped = append(dropped, id)
		}
	}
	m.proxyMu.Unlock()
	for _, id := range dropped {
		m.nodes.wakeOne(id)
	}
}

// ProxyCount reports how many proxies are currently live across all lanes.
func (m *Manager) ProxyCount() int {
	m.proxyMu.Lock()
	defer m.proxyMu.Unlock()
	n := 0
	for _, eps := range m.proxies {
		n += len(eps)
	}
	return n
}

// ProxyCounts reports how many proxies each lane currently has, keyed by lane ID
// (a lane with none is absent). Feeds the per-lane status badges in the panel.
func (m *Manager) ProxyCounts() map[string]int {
	m.proxyMu.Lock()
	defer m.proxyMu.Unlock()
	out := make(map[string]int, len(m.proxies))
	for id, eps := range m.proxies {
		out[id] = len(eps)
	}
	return out
}

// SeedProxies loads the proxy pool synchronously from current settings WITHOUT
// triggering a reconcile. Called once at startup, before the first reconcile, so
// Xray comes up a single time with the URL-sourced proxies already in place.
func (m *Manager) SeedProxies() {
	set, err := m.store.GetSettings()
	if err != nil {
		return
	}
	m.setProxies(m.buildProxies(set.Routing))
}

// RefreshProxies reloads the pool from current settings and reconciles if it
// changed. Runs on a timer and right after the routing config is saved.
func (m *Manager) RefreshProxies() {
	set, err := m.store.GetSettings()
	if err != nil {
		return
	}
	next := m.buildProxies(set.Routing)
	if proxiesEqual(next, m.getProxies()) {
		return
	}
	m.setProxies(next)
	m.TriggerReconcile()
}

// proxyLoop periodically refreshes the URL-sourced proxy pool on the operator's
// chosen cadence. When set to "never" it stays dormant but wakes periodically so
// re-enabling auto-refresh later takes effect without a restart. (Saving the
// routing config rebuilds the pool immediately via ApplyRouting, so a cadence
// change doesn't need to be picked up mid-sleep.)
func (m *Manager) proxyLoop() {
	for {
		d := m.currentProxyRefresh()
		if d <= 0 {
			time.Sleep(defaultProxyRefresh)
			continue
		}
		time.Sleep(d)
		if m.currentProxyRefresh() > 0 {
			m.RefreshProxies()
			m.RefreshNodeProxies()
		}
	}
}

// proxiesEqual reports whether a and b give every lane the same multiset of
// endpoints, ignoring order: each lane is health-balanced (Observatory) so the
// order in the config is irrelevant, and a source URL that returns the same
// proxies shuffled must not trigger a needless Xray restart.
func proxiesEqual(a, b map[string][]model.ProxyEndpoint) bool {
	if len(a) != len(b) {
		return false
	}
	for id, pa := range a {
		pb, ok := b[id]
		if !ok || !endpointsEqual(pa, pb) {
			return false
		}
	}
	return true
}

// endpointsEqual compares one lane's endpoints as an unordered multiset.
func endpointsEqual(a, b []model.ProxyEndpoint) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[model.ProxyEndpoint]int, len(a))
	for _, p := range a {
		counts[p]++
	}
	for _, p := range b {
		counts[p]--
		if counts[p] < 0 {
			return false // an endpoint in b not present (enough times) in a
		}
	}
	return true // equal lengths + no deficit ⇒ identical multisets
}
