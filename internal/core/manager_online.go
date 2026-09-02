package core

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// onlineGauge is who is connected to which server right now, kept in memory from
// the same sightings that feed the device count: the master's access log for
// itself, each node's report for that node. It exists for one reader — the
// subscription's server ordering, which wants "how loaded is this server" per
// fetch — and is deliberately not persisted: the connections table has no server
// column (a device is a device wherever it connects), and a gauge that rebuilds
// itself within a minute of a restart needs none.
type onlineGauge struct {
	mu   sync.Mutex
	seen map[int64]map[int64]int64 // server id → user id → last seen (unix)
}

// record notes that user was seen on server at ts.
func (g *onlineGauge) record(server, user, ts int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = map[int64]map[int64]int64{}
	}
	users := g.seen[server]
	if users == nil {
		users = map[int64]int64{}
		g.seen[server] = users
	}
	if ts > users[user] {
		users[user] = ts
	}
}

// counts returns, per server, how many distinct users were seen since `since`,
// dropping entries older than that on the way so the map never grows past the
// fleet's live users.
func (g *onlineGauge) counts(since int64) map[int64]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[int64]int, len(g.seen))
	for server, users := range g.seen {
		for user, ts := range users {
			if ts < since {
				delete(users, user)
				continue
			}
			out[server]++
		}
		if len(users) == 0 {
			delete(g.seen, server)
		}
	}
	return out
}

// RecordAccessOn is RecordAccess for a sighting that happened on a specific server
// (a node's report names the node; the master's own access log is server 0). The
// sighting feeds the device count exactly as before and, in addition, the online
// gauge for that server.
func (m *Manager) RecordAccessOn(serverID int64, email, ip, dest string) {
	if id, ok := userIDFromEmail(email); ok {
		m.online.record(serverID, id, time.Now().Unix())
		// Where they connected FROM, against the source policy. Rate-limited per
		// address inside, so this is a mutex and two lookups on the hot path.
		m.CheckConnPolicy(id, ip)
	}
	m.RecordAccess(email, ip, dest)
}

// RecordLocalAccess is the master's access-log hook: a sighting on the panel's own
// Xray, attributed to server 0.
func (m *Manager) RecordLocalAccess(email, ip, dest string) {
	m.RecordAccessOn(model.LocalNodeID, email, ip, dest)
}

// userIDFromEmail parses the "u<id>" Xray client tag.
func userIDFromEmail(email string) (int64, bool) {
	if !strings.HasPrefix(email, "u") {
		return 0, false
	}
	var id int64
	for _, c := range email[1:] {
		if c < '0' || c > '9' {
			return 0, false
		}
		id = id*10 + int64(c-'0')
		if id > 1<<40 {
			return 0, false
		}
	}
	return id, len(email) > 1
}

// OnlineByServer is how many distinct users each server has seen inside the
// online window, keyed by server id (0 = the master).
func (m *Manager) OnlineByServer() map[int64]int {
	return m.online.counts(time.Now().Unix() - model.DeviceOnlineWindow)
}

// CountryOfIP resolves a client address to its country code through the same
// geoip table the connection map uses. Blank when unknown or the table is not
// there yet — the caller degrades to ordering without it.
func (m *Manager) CountryOfIP(ip string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ""
	}
	lk := m.countryLookup()
	if lk == nil {
		return ""
	}
	cc, ok := lk.Lookup(addr)
	if !ok {
		return ""
	}
	return model.NormalizeCountry(cc)
}

// DetectHostCountry guesses where a server is from its address: an IP is looked up
// directly, a domain is resolved first (two seconds, best effort). Blank when it
// cannot tell. Used to pre-fill a server's country so the operator rarely has to
// type one; never overrides a value they set.
func (m *Manager) DetectHostCountry(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return m.CountryOfIP(addr.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if cc := m.CountryOfIP(a.Unmap().String()); cc != "" {
			return cc
		}
	}
	return ""
}

// SetMasterPlacement stores the master's own placement (see model.Placement); a
// blank country is filled from the panel's address when that can be resolved.
func (m *Manager) SetMasterPlacement(p model.Placement) error {
	// Validate the raw value: normalising first would quietly blank a mistyped
	// country instead of telling the operator.
	if err := p.Validate(); err != nil {
		return err
	}
	p = p.Normalized()
	if p.Country == "" {
		if set, err := m.store.GetSettings(); err == nil {
			p.Country = m.DetectHostCountry(set.Host)
		}
	}
	return m.store.SetMasterPlacement(p)
}
