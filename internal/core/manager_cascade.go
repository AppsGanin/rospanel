package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/proxypool"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Cascades (see xray/cascade.go): an entry server's cascade lane leaves through our
// own servers. Both ends are generated here, from one secret, so nothing about a
// bridge is stored: the entry gets links to its exits carrying a service credential,
// each exit gets that credential on its bridge lane, let in from the entry's
// addresses only. Changing the master's REALITY key changes every credential, and
// both ends follow on their next build.

// bridgeCreds is the service credential entry uses on exit: a version-4 UUID — so its
// route bytes never collide with an ext relay's (model.ExtRoute) — and a password.
func bridgeCreds(set *model.Settings, entry, exit int64) (uuid, password string) {
	mac := hmac.New(sha256.New, []byte("rospanel-bridge\x00"+set.RealityPrivateKey))
	mac.Write([]byte(strconv.FormatInt(entry, 10) + ">" + strconv.FormatInt(exit, 10)))
	sum := mac.Sum(nil)
	b := sum[:16]
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	uuid = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	return uuid, hex.EncodeToString(sum[16:])
}

// cascadeServer is one of our servers as a cascade end: its effective settings and,
// for a node, the row.
type cascadeServer struct {
	set  *model.Settings
	node *model.Node
}

// cascadeServers is every server that can be a cascade end: the master and every
// enabled node that has joined. The master's settings carry its certificate pin, as a
// subscription's do (server.applyTLSHints): a self-signed master is reached by its pin.
func (m *Manager) cascadeServers(set *model.Settings, nodes []model.Node) map[int64]cascadeServer {
	master := *set
	if m.tls.CertPath != "" {
		master.CertSHA256 = m.CertPinSHA256()
		if !m.HasValidCert() {
			master.TLSInsecure = true
			master.TLSPinSHA256 = master.CertSHA256
		}
	}
	out := map[int64]cascadeServer{model.LocalNodeID: {set: &master}}
	for i := range nodes {
		n := &nodes[i]
		if n.Enabled && n.Joined() && n.Host != "" {
			out[n.ID] = cascadeServer{set: nodeSettings(set, n), node: n}
		}
	}
	return out
}

// routingOf is a server's own routing: the master's settings, or a node's row.
func routingOf(s cascadeServer) *model.RoutingConfig {
	if s.node != nil {
		return s.node.Routing
	}
	return &s.set.Routing
}

// cascadeFor is the upstreams of server's cascade lanes: a link to each exit over the
// lane's bridge. An exit that is gone, or whose bridge lane is off, is left out; a lane
// left with none is unusable, and being strict drops its traffic.
func (m *Manager) cascadeFor(server int64, servers map[int64]cascadeServer, master *model.Settings) map[string][]model.ProxyEndpoint {
	self, ok := servers[server]
	if !ok {
		return nil
	}
	rc := routingOf(self)
	if rc == nil {
		return nil
	}
	var out map[string][]model.ProxyEndpoint
	for _, lane := range rc.Lanes {
		if !lane.Enabled || !lane.IsCascade() {
			continue
		}
		var eps []model.ProxyEndpoint
		for _, e := range lane.Via {
			x, ok := servers[e.Server]
			if !ok || e.Server == server {
				continue
			}
			uuid, pass := bridgeCreds(master, server, e.Server)
			if l := bridgeLink(e.BridgeOr(), model.User{UUID: uuid, Password: pass}, m.overIPv4(x.set)); l != "" {
				eps = append(eps, proxypool.Parse([]string{l})...)
			}
		}
		if len(eps) > 0 {
			if out == nil {
				out = map[string][]model.ProxyEndpoint{}
			}
			out[lane.ID] = eps
		}
	}
	return out
}

// bridgeLink is the link to an exit over a bridge lane, "" when that lane is off there.
func bridgeLink(bridge string, u model.User, exit *model.Settings) string {
	switch bridge {
	case model.BridgeVision:
		if exit.VLESSEnabled {
			return link.VLESS(u, exit)
		}
	case model.BridgeHysteria:
		if exit.HysteriaEnabled {
			return link.Hysteria2(u, exit)
		}
	default:
		if exit.RealityEnabled && exit.RealityPublicKey != "" {
			return link.Reality(u, exit)
		}
	}
	return ""
}

// bridgesFor is the cascade entries that leave through exit: every server with an
// enabled cascade lane via it, on that lane's bridge, from the entry's addresses.
func (m *Manager) bridgesFor(exit int64, servers map[int64]cascadeServer, master *model.Settings) []xray.Bridge {
	var out []xray.Bridge
	ids := make([]int64, 0, len(servers))
	for id := range servers {
		ids = append(ids, id)
	}
	slices.Sort(ids) // a stable order: the config is fingerprinted
	for _, entry := range ids {
		if entry == exit {
			continue
		}
		rc := routingOf(servers[entry])
		if rc == nil {
			continue
		}
		seen := map[string]bool{}
		for _, lane := range rc.Lanes {
			if !lane.Enabled {
				continue
			}
			for _, e := range lane.Via {
				if e.Server != exit || seen[e.BridgeOr()] {
					continue
				}
				seen[e.BridgeOr()] = true
				uuid, pass := bridgeCreds(master, entry, exit)
				out = append(out, xray.Bridge{
					Entry: entry, Email: model.BridgeEmail(entry), UUID: uuid, Password: pass,
					Lane: e.BridgeOr(), Sources: m.entryAddrs(servers[entry]),
				})
			}
		}
	}
	return out
}

// overIPv4 is an exit's settings with its host name swapped for its IPv4 address, so
// the entry dials it over IPv4 — from the address the exit knows it by (entryAddrs),
// not an IPv6 one it may have never shown. The name stays where TLS needs it (SNI);
// an exit with no IPv4 address is left as it is.
func (m *Manager) overIPv4(s *model.Settings) *model.Settings {
	if _, err := netip.ParseAddr(s.Host); err == nil || m.hosts == nil {
		return s
	}
	for _, a := range m.hosts.lookup(s.Host) {
		if a.Unmap().Is4() {
			c := *s
			if c.SNI == "" {
				c.SNI = s.Host
			}
			c.Host = a.Unmap().String()
			return &c
		}
	}
	return s
}

// entryAddrs is where an entry's traffic comes from: its host's addresses; for a node,
// the ones it last reached the panel from, one per family — a server behind NAT leaves
// by those; for the master, its own public interface addresses besides, so a failed
// lookup of its name does not leave it with none.
func (m *Manager) entryAddrs(s cascadeServer) []string {
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(s.set.Host); err == nil {
		addrs = append(addrs, a.Unmap())
	} else if m.hosts != nil {
		addrs = append(addrs, m.hosts.lookup(s.set.Host)...)
	}
	if s.node != nil {
		for _, f := range strings.Fields(s.node.SeenIP) {
			if a, err := netip.ParseAddr(f); err == nil {
				addrs = append(addrs, a.Unmap())
			}
		}
	} else {
		addrs = append(addrs, publicInterfaceAddrs()...)
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// cascadeOpts fills a server's options with its cascade lanes' upstreams and the
// entries it is the exit of.
func (m *Manager) cascadeOpts(opts *xray.Options, server int64, master *model.Settings) {
	nodes, err := m.store.ListNodes()
	if err != nil {
		logErr("cascade: nodes", "err", err)
		return
	}
	servers := m.cascadeServers(master, nodes)
	opts.Cascade = m.cascadeFor(server, servers, master)
	opts.Bridges = m.bridgesFor(server, servers, master)
}

// NoteNodeSeenIP records the address a node reached the panel from, one per family
// ("v4 v6"): a node that reaches it over both does not flip the record — and with it
// its exits' configs — on every sync. A change moves the cascade exits that let it in.
func (m *Manager) NoteNodeSeenIP(n *model.Node, ip string) {
	a, err := netip.ParseAddr(ip)
	if err != nil || a.IsLoopback() {
		return
	}
	a = a.Unmap()
	var v4, v6 string
	for _, f := range strings.Fields(n.SeenIP) {
		if p, err := netip.ParseAddr(f); err == nil && p.Is4() {
			v4 = f
		} else if err == nil {
			v6 = f
		}
	}
	if a.Is4() {
		v4 = a.String()
	} else {
		v6 = a.String()
	}
	next := strings.TrimSpace(v4 + " " + v6)
	if next == n.SeenIP {
		return
	}
	if err := m.store.SetNodeSeenIP(n.ID, next); err != nil {
		logErr("nodes: seen address", "node", n.ID, "err", err)
		return
	}
	if m.anyCascade() {
		m.cascadeMoved()
	}
}

// anyCascade reports whether any server has a cascade lane.
func (m *Manager) anyCascade() bool {
	set, err := m.store.GetSettings()
	if err != nil {
		return true // unknown: rebuilding is the safe side
	}
	nodes, err := m.store.ListNodes()
	if err != nil {
		return true
	}
	for _, s := range m.cascadeServers(set, nodes) {
		if rc := routingOf(s); rc != nil {
			for _, l := range rc.Lanes {
				if l.IsCascade() {
					return true
				}
			}
		}
	}
	return false
}

// cascadeMoved rebuilds every server a cascade change can reach: an entry's lane names
// its exits, an exit's settings shape the entry's links — and the master's flood guard
// lets its entries in.
func (m *Manager) cascadeMoved() {
	m.TriggerReconcile()
	if m.nodes != nil {
		m.nodes.wakeAll()
	}
	m.ensureLocalConnGuard()
}

// bridgeSources is every address the bridges come from, for the flood guard.
func bridgeSources(bridges []xray.Bridge) []string {
	var out []string
	for _, b := range bridges {
		out = append(out, b.Sources...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// localBridgeSources is the addresses of the master's cascade entries.
func (m *Manager) localBridgeSources(set *model.Settings) []string {
	nodes, err := m.store.ListNodes()
	if err != nil {
		return nil
	}
	return bridgeSources(m.bridgesFor(model.LocalNodeID, m.cascadeServers(set, nodes), set))
}

// validateCascade checks a server's cascade lanes before they are saved: every exit is
// one of our servers in service, not this one, with the connection switched on, and no
// exit leads back here. Only a lane that is new or changed against prev is checked: an
// exit that went away later must not block a rename of this server.
func (m *Manager) validateCascade(server int64, rc, prev *model.RoutingConfig) error {
	unchanged := func(l model.EgressLane) bool {
		if prev == nil {
			return false
		}
		for _, p := range prev.Lanes {
			if p.ID == l.ID && p.Enabled == l.Enabled && slices.Equal(p.Via, l.Via) {
				return true
			}
		}
		return false
	}
	var servers map[int64]cascadeServer
	for _, lane := range rc.Lanes {
		if !lane.Enabled || !lane.IsCascade() || unchanged(lane) {
			continue
		}
		if servers == nil {
			set, err := m.store.GetSettings()
			if err != nil {
				return err
			}
			nodes, err := m.store.ListNodes()
			if err != nil {
				return err
			}
			servers = m.cascadeServers(set, nodes)
		}
		for _, e := range lane.Via {
			if e.Server == server {
				return invalidCode("err.cascadeSelf", "полоса «{{lane}}»: сервер не может быть выходом сам для себя",
					map[string]any{"lane": lane.Name})
			}
			x, ok := servers[e.Server]
			if !ok {
				return invalidCode("err.cascadeNoServer", "полоса «{{lane}}»: сервер-выход не найден, выключен или ещё не подключён",
					map[string]any{"lane": lane.Name})
			}
			if bridgeLink(e.BridgeOr(), probeUser, x.set) == "" {
				return invalidCode("err.cascadeLaneOff", "полоса «{{lane}}»: на сервере «{{server}}» выключено подключение «{{bridge}}», через которое идёт каскад",
					map[string]any{"lane": lane.Name, "server": serverName(x), "bridge": bridgeNames(x.set)[e.BridgeOr()]})
			}
			// The exit's own cascades must not lead back here: bridged traffic follows the
			// exit's routing, and a loop would pass it round until it times out.
			if loopsBack(e.Server, server, servers, map[int64]bool{}) {
				return invalidCode("err.cascadeLoop", "полоса «{{lane}}»: через сервер «{{server}}» каскад возвращается сюда же",
					map[string]any{"lane": lane.Name, "server": serverName(x)})
			}
		}
	}
	return nil
}

// probeUser stands in for a credential where only whether a link can be built matters.
var probeUser = model.User{UUID: "00000000-0000-4000-8000-000000000000", Password: "x"}

func serverName(s cascadeServer) string {
	if s.node != nil {
		return s.node.Name
	}
	return model.LocalNodeName
}

// loopsBack reports whether from's enabled cascades reach target.
func loopsBack(from, target int64, servers map[int64]cascadeServer, seen map[int64]bool) bool {
	if from == target {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	s, ok := servers[from]
	if !ok {
		return false
	}
	rc := routingOf(s)
	if rc == nil {
		return false
	}
	for _, l := range rc.Lanes {
		if !l.Enabled {
			continue
		}
		for _, e := range l.Via {
			if loopsBack(e.Server, target, servers, seen) {
				return true
			}
		}
	}
	return false
}

// exitInUse refuses to switch off or delete a server some cascade leaves through.
func (m *Manager) exitInUse(server int64) error {
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	nodes, err := m.store.ListNodes()
	if err != nil {
		return err
	}
	servers := m.cascadeServers(set, nodes)
	if bridges := m.bridgesFor(server, servers, set); len(bridges) > 0 {
		return invalidCode("err.cascadeExitInUse", "этот сервер — выход каскада для «{{entry}}»; сначала уберите его из каскада на том сервере",
			map[string]any{"entry": serverName(servers[bridges[0].Entry])})
	}
	return nil
}

// nodeEditKeepsBridges is exitKeepsBridges for a node edit that switches its lanes.
func (m *Manager) nodeEditKeepsBridges(id int64, e store.NodeEdit) error {
	n, err := m.store.GetNode(id)
	if err != nil || n == nil {
		return err
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	next := *n
	if e.VLESS != nil {
		next.VLESSEnabled = e.VLESS
	}
	if e.Hysteria != nil {
		next.HysteriaEnabled = e.Hysteria
	}
	if e.Reality != nil {
		next.RealityEnabled = e.Reality
	}
	return m.exitKeepsBridges(id, set, nodeSettings(set, &next))
}

// cascadeChanged is cascadeMoved when there is a cascade at all.
func (m *Manager) cascadeChanged() {
	if m.anyCascade() {
		m.cascadeMoved()
	}
}

// exitKeepsBridges refuses a connections save that switches off the lane a cascade
// entry reaches this server over: the entry's lane would lose its exit and its
// traffic, strict, would stop without a word. next is the settings after the save.
func (m *Manager) exitKeepsBridges(server int64, master, next *model.Settings) error {
	nodes, err := m.store.ListNodes()
	if err != nil {
		return err
	}
	servers := m.cascadeServers(master, nodes)
	for _, b := range m.bridgesFor(server, servers, master) {
		if bridgeLink(b.Lane, probeUser, next) != "" {
			continue
		}
		lane := bridgeNames(next)[b.Lane]
		name := model.LocalNodeName
		if s, ok := servers[b.Entry]; ok && s.node != nil {
			name = s.node.Name
		}
		return invalidCode("err.cascadeExitLaneOff", "этот сервер — выход каскада для «{{entry}}» через подключение «{{bridge}}»; сначала уберите его из каскада на том сервере",
			map[string]any{"entry": name, "bridge": lane})
	}
	return nil
}

// BridgeOption is one connection a cascade can reach a server over.
type BridgeOption struct {
	Bridge  string `json:"bridge"`  // the value for CascadeExit.Bridge
	Name    string `json:"name"`    // what this server calls it
	Enabled bool   `json:"enabled"` // on there: a cascade can use it
}

// bridgeOptions is a server's bridge connections, in the order a picker offers them.
func bridgeOptions(s *model.Settings) []BridgeOption {
	names := bridgeNames(s)
	out := make([]BridgeOption, 0, 3)
	for _, b := range []string{model.BridgeReality, model.BridgeVision, model.BridgeHysteria} {
		out = append(out, BridgeOption{Bridge: b, Name: names[b], Enabled: bridgeLink(b, probeUser, s) != ""})
	}
	return out
}

// bridgeNames is a server's names for its bridge connections, as its users see them.
func bridgeNames(s *model.Settings) map[string]string {
	return map[string]string{
		model.BridgeReality:  s.ProtoLabel(model.ProtoReality),
		model.BridgeVision:   s.ProtoLabel(model.ProtoVLESS),
		model.BridgeHysteria: s.ProtoLabel(model.ProtoHysteria),
	}
}

// publicInterfaceAddrs is this machine's public addresses, as its interfaces carry them.
func publicInterfaceAddrs() []netip.Addr {
	ifaddrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, ia := range ifaddrs {
		ipn, ok := ia.(*net.IPNet)
		if !ok {
			continue
		}
		a, ok := netip.AddrFromSlice(ipn.IP)
		if !ok {
			continue
		}
		a = a.Unmap()
		if a.IsGlobalUnicast() && !a.IsPrivate() && !model.IsPrivateEgress(a) {
			out = append(out, a)
		}
	}
	return out
}

// bridgesKey says which bridges, on which lanes and from where, a config was built with.
func bridgesKey(bridges []xray.Bridge) string {
	var b strings.Builder
	for _, x := range bridges {
		b.WriteString(x.Email + "|" + x.Lane + "|" + x.UUID + "|" + strings.Join(x.Sources, ",") + ";")
	}
	return b.String()
}
