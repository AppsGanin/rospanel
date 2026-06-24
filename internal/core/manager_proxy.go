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

// buildProxies parses the manual proxy list and merges in whatever the source
// URLs serve (best-effort per-URL fetch — failures are skipped).
func (m *Manager) buildProxies(rc model.RoutingConfig) []model.ProxyEndpoint {
	lines := append([]string{}, rc.ProxyManual...)
	for _, url := range rc.ProxyURLs {
		if url = strings.TrimSpace(url); url == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		fetched, err := proxypool.Fetch(ctx, url)
		cancel()
		if err != nil {
			logWarn("proxypool: fetch failed", "url", url, "err", err)
			continue
		}
		lines = append(lines, fetched...)
	}
	return proxypool.Parse(lines)
}

func (m *Manager) getProxies() []model.ProxyEndpoint {
	m.proxyMu.Lock()
	defer m.proxyMu.Unlock()
	return append([]model.ProxyEndpoint(nil), m.proxies...)
}

func (m *Manager) setProxies(p []model.ProxyEndpoint) {
	m.proxyMu.Lock()
	m.proxies = p
	m.proxyMu.Unlock()
}

// ProxyCount reports how many proxies are currently in the pool (parsed from the
// URL + manual sources).
func (m *Manager) ProxyCount() int {
	m.proxyMu.Lock()
	defer m.proxyMu.Unlock()
	return len(m.proxies)
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
		}
	}
}

// proxiesEqual reports whether a and b are the same multiset of endpoints,
// ignoring order: the pool is health-balanced (Observatory) so the order in the
// config is irrelevant, and a source URL that returns the same proxies shuffled
// must not trigger a needless Xray restart.
func proxiesEqual(a, b []model.ProxyEndpoint) bool {
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
