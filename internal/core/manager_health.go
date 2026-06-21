package core

import (
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// laneHealthInterval is how often the Opera lane is probed for liveness.
const laneHealthInterval = 20 * time.Second

// laneHealth tracks whether each helper-backed egress lane can currently reach
// the internet (i.e. is "alive" vs. Xray having fallen the lane back to direct).
type laneHealth struct {
	mu    sync.Mutex
	opera bool
}

// healthLoop periodically probes the enabled Opera lane through its local proxy
// and records whether it's alive, so the dashboard/routing UI can show
// "активен" vs "на фолбэке".
func (m *Manager) healthLoop() {
	t := time.NewTicker(laneHealthInterval)
	defer t.Stop()
	for {
		m.probeLanes()
		<-t.C
	}
}

func (m *Manager) probeLanes() {
	set, err := m.store.GetSettings()
	if err != nil {
		return
	}
	opera := set.OperaEnabled && m.operaSup.Running() && probeProxyHealth(set.OperaPortOr())
	m.health.mu.Lock()
	m.health.opera = opera
	m.health.mu.Unlock()
}

// OperaHealthy reports the last probe result for the Opera lane.
func (m *Manager) OperaHealthy() bool {
	m.health.mu.Lock()
	defer m.health.mu.Unlock()
	return m.health.opera
}

// probeProxyHealth reports whether an HTTP CONNECT to a known 204 endpoint
// succeeds through the local proxy at 127.0.0.1:port — the same signal Xray's
// Observatory uses to decide the lane's fallback.
func probeProxyHealth(port int) bool {
	proxyURL, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		return false
	}
	client := &http.Client{
		Timeout:   8 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}
	resp, err := client.Get("https://www.google.com/generate_204")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK
}
