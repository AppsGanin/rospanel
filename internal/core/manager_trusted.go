package core

import (
	"sync"

	"github.com/AppsGanin/rospanel/internal/ipblock"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Trusted networks (model.TrustedNets): addresses no automatic ban may touch. Three
// things drop an address at the firewall without an operator asking — the proxy
// brute-force guard, the scanner block and the source policy — and each asks
// Trusted before it does.

// trustedState caches the parsed list: the policy asks on the connection path and
// must not read the database per sighting.
type trustedState struct {
	// saveMu makes a save one step: the stored list and the cached one are written
	// under it together, so two saves racing cannot leave the cache holding the list
	// that lost in the database.
	saveMu sync.Mutex
	mu     sync.RWMutex
	loaded bool
	nets   model.TrustedNets
	list   []string
}

func (m *Manager) trustedLoad() {
	m.trusted.mu.RLock()
	loaded := m.trusted.loaded
	m.trusted.mu.RUnlock()
	if loaded {
		return
	}
	list, err := m.store.TrustedNets()
	if err != nil {
		return // unreadable settings trust nobody, and are asked again next time
	}
	m.trusted.mu.Lock()
	if !m.trusted.loaded {
		m.trusted.list = list
		m.trusted.nets = model.ParseTrustedNets(list)
		m.trusted.loaded = true
	}
	m.trusted.mu.Unlock()
}

// TrustedNets returns the stored list, normalized.
func (m *Manager) TrustedNets() []string {
	m.trustedLoad()
	m.trusted.mu.RLock()
	defer m.trusted.mu.RUnlock()
	return append([]string{}, m.trusted.list...)
}

// Trusted reports whether ip falls inside a trusted network.
func (m *Manager) Trusted(ip string) bool {
	m.trustedLoad()
	m.trusted.mu.RLock()
	defer m.trusted.mu.RUnlock()
	return m.trusted.nets.Contains(ip)
}

// SaveTrustedNets validates, stores and applies the list, then lifts whatever the
// automatic bans already hold inside it. An operator adding their office because it
// was just banned means "let us back in", not "don't do it again in an hour".
func (m *Manager) SaveTrustedNets(entries []string) error {
	list, err := model.NormalizeTrustedNets(entries)
	if err != nil {
		return err
	}
	m.trusted.saveMu.Lock()
	defer m.trusted.saveMu.Unlock()
	if err := m.store.SetTrustedNets(list); err != nil {
		return err
	}
	nets := model.ParseTrustedNets(list)
	m.trusted.mu.Lock()
	m.trusted.list, m.trusted.nets, m.trusted.loaded = list, nets, true
	m.trusted.mu.Unlock()

	// The source policy's blocks are recorded and handed to the nodes, so they come
	// out the way an operator's own unblock does: the row, the firewall, the fleet.
	if ips, err := m.store.BlockedIPList(); err == nil {
		for _, ip := range ips {
			if nets.Contains(ip) {
				if _, err := m.UnblockIP(ip); err != nil {
					logErr("trusted: could not lift a policy block", "ip", ip, "err", err)
				}
			}
		}
	}
	// The other two live only in this machine's kernel.
	liftTrusted(m.probeBlock, nets, "scanner")
	if m.guard != nil {
		liftTrusted(m.guard.blocker, nets, "brute-force")
		m.guard.forget(nets)
	}
	return nil
}

// liftTrusted removes every address inside nets from one block table.
func liftTrusted(b *ipblock.Blocker, nets model.TrustedNets, what string) {
	ips, err := b.Addresses()
	if err != nil {
		logErr("trusted: could not read a block table", "table", what, "err", err)
		return
	}
	for _, ip := range ips {
		if !nets.Contains(ip) {
			continue
		}
		if err := b.UnblockIP(ip); err != nil {
			logErr("trusted: could not lift a block", "table", what, "ip", ip, "err", err)
		}
	}
}
