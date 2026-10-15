package core

import (
	"fmt"
	"hash/fnv"
	"net"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/mtproto"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// syncMTProto puts the master's MTProto lane where the settings say, with the users of
// the config just put in force; that ends the open connections of anyone it no longer
// holds. A listener that cannot start is reported on the Connections screen and logged
// once per change, and tried again on the next sync.
func (m *Manager) syncMTProto(set *model.Settings, cfg *xray.Config) {
	if set.MTProtoEnabled && set.MTProtoPort == 0 {
		m.claimMTProtoPort(set)
	}
	if set.MTProtoEnabled && set.MTProtoDomain == "" {
		if kept, err := m.store.ClaimMTProtoDomain(set.MTProtoAutoDomain()); err == nil {
			set.MTProtoDomain = kept
		} else {
			logErr("mtproto: could not keep the domain", "err", err)
		}
	}
	clients, addr := xray.MTProtoClients(cfg)
	l, err := mtprotoClearOfXray(MTProtoListen(set), xray.TCPListenerPorts(cfg))
	if applyErr := m.mtproto.Apply(l, addr, mtprotoUsers(clients)); err == nil {
		err = applyErr
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if prev := m.mtprotoErr.Swap(&msg); prev == nil || *prev != msg {
		if err != nil {
			logErr("mtproto: listener failed", "err", err)
		}
	}
}

// mtprotoClearOfXray is l, or off with an error when Xray listens on its port (see
// mieruClearOfXray).
func mtprotoClearOfXray(l mtproto.Listen, xrayPorts map[int]string) (mtproto.Listen, error) {
	if tag, taken := xrayPorts[l.Port]; l.Port > 0 && taken {
		return mtproto.Listen{}, fmt.Errorf("mtproto: port %d is Xray's (%s); pick another", l.Port, tag)
	}
	return l, nil
}

// MTProtoListen is where a server's settings put its MTProto lane; off is a zero Listen.
//
// The domain in the secrets names the server itself — its own domain, or a name that
// resolves to its address — and anything that is not a client is handed to the server's
// own :443: the decoy, with its certificate. A domain an operator set to another site
// hands strangers to that site.
func MTProtoListen(set *model.Settings) mtproto.Listen {
	if !set.MTProtoEnabled || set.MTProtoPort <= 0 {
		return mtproto.Listen{}
	}
	domain, front := set.MTProtoDomainOr(), ""
	if set.MTProtoOwnDomain(domain) && set.VLESSPort > 0 {
		front = net.JoinHostPort("127.0.0.1", strconv.Itoa(set.VLESSPort))
	} else if domain != "" {
		front = net.JoinHostPort(domain, "443")
	}
	return mtproto.Listen{Port: set.MTProtoPort, Domain: domain, Front: front}
}

// resolveMTProtoDomain checks a domain an operator entered for the lane: "" keeps the
// one kept.
func resolveMTProtoDomain(domain string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(domain))
	if d == "" {
		return "", nil
	}
	if !realityHostRe.MatchString(d) {
		return "", invalidCode("err.mtprotoDomain", "домен {{value}} не похож на настоящий", map[string]any{"value": d})
	}
	return d, nil
}

// mtprotoUsers turns mtproto-in's clients into the lane's accounts.
func mtprotoUsers(clients []xray.VLESSClient) []mtproto.User {
	out := make([]mtproto.User, 0, len(clients))
	for _, c := range clients {
		out = append(out, mtproto.User{Email: c.Email, UUID: c.ID})
	}
	return out
}

// MTProtoStatus reports whether the master's MTProto lane listens, and the last error.
func (m *Manager) MTProtoStatus() (running bool, lastErr string) {
	if m.mtproto == nil {
		return false, ""
	}
	if p := m.mtprotoErr.Load(); p != nil {
		lastErr = *p
	}
	return m.mtproto.Listening().Port > 0, lastErr
}

// StopMTProto closes the master's MTProto lane (shutdown).
func (m *Manager) StopMTProto() {
	if m.mtproto != nil {
		m.mtproto.Close()
	}
}

// RetryMTProto starts the master's MTProto listener when the last sync could not (see
// RetryMieru).
func (m *Manager) RetryMTProto() {
	if m.mtproto == nil {
		return
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if p := m.mtprotoErr.Load(); p == nil || *p == "" {
		return
	}
	msg := ""
	if err := m.mtproto.Retry(); err != nil {
		msg = err.Error()
	} else if m.mtproto.Listening().Port == 0 {
		return
	}
	m.mtprotoErr.Store(&msg)
}

// resolveMTProtoPort checks the port an operator entered for a server's MTProto lane
// and settles it, as resolveMieruPort does for mieru (TCP only).
func resolveMTProtoPort(port int, on, wasOn bool, curPort int, held model.ReservedPorts, local bool) (int, error) {
	if port < 0 || port > 65535 {
		return 0, invalidCode("err.mtprotoPortRange", "порт MTProto вне диапазона 1–65535")
	}
	if port == 0 {
		port = curPort
	}
	if !on {
		return port, nil
	}
	if port == 0 {
		port = clearOf(held, model.MieruTCP, pickFreePort("tcp"))
	}
	if who, taken := held.OnTCP(port); held.TCP != nil && taken {
		return 0, invalidCode("err.portTakenBy", "порт {{port}} уже занят ({{who}}) — выберите другой", map[string]any{"port": port, "who": who})
	}
	ownPort := wasOn && port == curPort
	if local && !ownPort && !portFree("tcp", port) {
		return 0, invalidCode("err.tcpPortTaken", "TCP-порт {{port}} уже занят — выберите другой", map[string]any{"port": port})
	}
	return port, nil
}

// mtprotoHeldOn is what else listens on a server whose settings are set, the MTProto
// lane itself left out (see mieruHeldOn).
func mtprotoHeldOn(set *model.Settings, custom []model.Inbound) model.ReservedPorts {
	other := *set
	other.MTProtoEnabled = false
	return withCustomPorts(reservedPorts(&other), custom)
}

// withCustomPorts adds the custom inbounds' ports to r.
func withCustomPorts(r model.ReservedPorts, custom []model.Inbound) model.ReservedPorts {
	for _, in := range custom {
		if in.Port > 0 {
			if model.ProtoOf(in.Protocol) == "udp" {
				r.HoldUDP(in.Port, in.Name)
			} else {
				r.HoldTCP(in.Port, in.Name)
			}
		}
	}
	return r
}

// claimMTProtoPort gives the master's lane, switched on without a port (the default),
// one free here and held by nothing else of the server's, and keeps it (see
// claimMieruPort). set is updated in place.
func (m *Manager) claimMTProtoPort(set *model.Settings) {
	custom, _ := m.store.Inbounds(model.LocalNodeID)
	held := mtprotoHeldOn(set, custom)
	port := clearOf(held, model.MieruTCP, defaultMTProtoPort(set.RealityPublicKey))
	if !portFree("tcp", port) {
		port = clearOf(held, model.MieruTCP, pickFreePort("tcp"))
	}
	kept, err := m.store.ClaimMTProtoPort(port)
	if err != nil {
		logErr("mtproto: could not keep the picked port", "err", err)
		return
	}
	set.MTProtoPort = kept
	m.ensureLocalConnGuard() // the new public port joins the per-IP guard
}

// defaultMTProtoPort is a server's MTProto port when none was chosen, derived from
// something unique to the server (see defaultMieruPort).
func defaultMTProtoPort(seed string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte("mtproto:" + seed))
	return mieruPortMin + int(h.Sum32()%uint32(mieruPortMax-mieruPortMin))
}

// claimNodeMTProtoDomain keeps the domain a node's MTProto lane runs with, when none is
// kept yet: from then on a changed host or REALITY donor (the master's, which a node may
// inherit) leaves the users' links as they are. Not on the sync path — a write there
// would change the node's inputs under the state being built.
func (m *Manager) claimNodeMTProtoDomain(n *model.Node) {
	if n == nil || n.MTProtoDomain != "" || !derefBool(n.MTProtoEnabled) {
		return
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return
	}
	if err := m.store.ClaimNodeMTProtoDomain(n.ID, nodeSettings(set, n).MTProtoAutoDomain()); err != nil {
		logErr("mtproto: could not keep a node's domain", "node", n.ID, "err", err)
	}
}

// claimNodeMTProtoDomains keeps the domain of every node that runs the lane without one
// (an upgraded install), at startup.
func (m *Manager) claimNodeMTProtoDomains() {
	nodes, err := m.store.ListNodes()
	if err != nil {
		return
	}
	for i := range nodes {
		m.claimNodeMTProtoDomain(&nodes[i])
	}
}
