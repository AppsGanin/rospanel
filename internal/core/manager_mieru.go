package core

import (
	"fmt"
	"hash/fnv"
	"time"

	"github.com/AppsGanin/rospanel/internal/mieru"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// syncMieru puts the master's mieru lane where the settings say, with the users of the
// config just put in force; that ends the open sessions of anyone it no longer holds.
// A listener that cannot start (the port taken) is reported on the Connections screen
// and logged once per change, and tried again on the next sync.
func (m *Manager) syncMieru(set *model.Settings, cfg *xray.Config) {
	if set.MieruEnabled && set.MieruPort == 0 {
		m.claimMieruPort(set)
	}
	clients, addr := xray.MieruClients(cfg)
	l, err := mieruClearOfXray(mieruListen(set), xray.TCPListenerPorts(cfg))
	if applyErr := m.mieru.Apply(l, addr, mieruUsers(clients)); err == nil {
		err = applyErr
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if prev := m.mieruErr.Swap(&msg); prev == nil || *prev != msg {
		if err != nil {
			logErr("mieru: listener failed", "err", err)
		}
	}
}

// mieruClearOfXray is l, or off with an error when Xray listens on its port: whichever
// of the two binds first would keep the other off it, and a mieru listener retried in
// the gap of an Xray restart would take Xray down. The settings refuse such a port; this
// is for one that got there anyway (an inbound made before, a hand-edited row).
func mieruClearOfXray(l mieru.Listen, xrayPorts map[int]string) (mieru.Listen, error) {
	if tag, taken := xrayPorts[l.Port]; l.Port > 0 && taken {
		return mieru.Listen{}, fmt.Errorf("mieru: port %d is Xray's (%s); pick another", l.Port, tag)
	}
	return l, nil
}

// mieruListen is where a server's settings put its mieru lane; off is a zero Listen.
func mieruListen(set *model.Settings) mieru.Listen {
	if !set.MieruEnabled || set.MieruPort <= 0 {
		return mieru.Listen{}
	}
	return mieru.Listen{Port: set.MieruPort, Transport: model.MieruTransportOr(set.MieruTransport)}
}

// mieruUsers turns mieru-in's clients into the lane's credentials.
func mieruUsers(clients []xray.VLESSClient) []mieru.User {
	out := make([]mieru.User, 0, len(clients))
	for _, c := range clients {
		out = append(out, mieru.User{Email: c.Email, UUID: c.ID})
	}
	return out
}

// MieruStatus reports whether the master's mieru lane listens, and the last error.
func (m *Manager) MieruStatus() (running bool, lastErr string) {
	if m.mieru == nil {
		return false, ""
	}
	if p := m.mieruErr.Load(); p != nil {
		lastErr = *p
	}
	return m.mieru.Listening().Port > 0, lastErr
}

// StopMieru closes the master's mieru lane (shutdown).
func (m *Manager) StopMieru() {
	if m.mieru != nil {
		m.mieru.Close()
	}
}

// resolveMieruPort checks the port and transport an operator entered for a server's
// mieru lane and settles them: 0 keeps the port the lane has, or picks one when it has
// none and is being switched on. held is what else listens on that server (built-in
// lanes, the hop range, custom inbounds — see otherListenerPorts); the port must not be
// among it. local says the lane runs in this process (the master's), so a port it is
// about to take must also be free here — a node's is checked by the node's own agent,
// which reports a listener that cannot start.
func resolveMieruPort(port int, transport string, on, wasOn bool, curPort int, curTransport string, held model.ReservedPorts, local bool) (int, string, error) {
	if port < 0 || port > 65535 {
		return 0, "", invalidCode("err.mieruPortRange", "порт mieru вне диапазона 1–65535")
	}
	switch transport {
	case "":
		transport = curTransport
	case model.MieruTCP:
	default: // UDP included: see model.MieruTransportOr
		return 0, "", invalidCode("err.mieruTransport", "mieru работает только по TCP")
	}
	transport = model.MieruTransportOr(transport)
	if port == 0 {
		port = curPort
	}
	if !on {
		return port, transport, nil
	}
	network := mieruNetwork(transport)
	if port == 0 {
		port = clearOf(held, transport, pickFreePort(network))
	}
	if who, taken := mieruHeld(held, transport, port); taken {
		return 0, "", invalidCode("err.portTakenBy", "порт {{port}} уже занят ({{who}}) — выберите другой", map[string]any{"port": port, "who": who})
	}
	ownPort := wasOn && port == curPort && transport == curTransport // our own listener has it
	if local && !ownPort && !portFree(network, port) {
		if network == "udp" {
			return 0, "", invalidCode("err.udpPortTaken", "UDP-порт {{port}} уже занят — выберите другой", map[string]any{"port": port})
		}
		return 0, "", invalidCode("err.tcpPortTaken", "TCP-порт {{port}} уже занят — выберите другой", map[string]any{"port": port})
	}
	return port, transport, nil
}

func mieruNetwork(transport string) string {
	if model.MieruTransportOr(transport) == model.MieruUDP {
		return "udp"
	}
	return "tcp"
}

// mieruHeld reports who else holds port on the network mieru would take it on.
func mieruHeld(held model.ReservedPorts, transport string, port int) (string, bool) {
	if held.TCP == nil {
		return "", false
	}
	if mieruNetwork(transport) == "udp" {
		return held.OnUDP(port)
	}
	return held.OnTCP(port)
}

// The range mieru ports are picked from: high, and below Linux's ephemeral ports
// (32768+), which an outgoing connection may hold at the moment of a rebind.
const (
	mieruPortMin = 10000
	mieruPortMax = 30000 // exclusive
)

// clearOf is port, or the next one after it (within the pick range) nothing else on the
// server holds.
func clearOf(held model.ReservedPorts, transport string, port int) int {
	for range mieruPortMax - mieruPortMin {
		if _, taken := mieruHeld(held, transport, port); !taken {
			return port
		}
		if port++; port >= mieruPortMax {
			port = mieruPortMin
		}
	}
	return port
}

// mieruHeldOn is what else listens on a server whose settings are set: its listeners —
// the system proxy's included — and custom inbounds, the mieru lane itself left out.
func mieruHeldOn(set *model.Settings, custom []model.Inbound) model.ReservedPorts {
	other := *set
	other.MieruEnabled = false
	r := reservedPorts(&other)
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

// withConnPorts is s with the ports and switches a connections save is about to put in
// place — what mieru's port is checked against, rather than what they were before.
func withConnPorts(s model.Settings, u ConnectionsUpdate, awgPort int) *model.Settings {
	on := func(key string, cur bool) bool {
		if v, ok := u.Protocols[key]; ok {
			return v
		}
		return cur
	}
	s.VLESSEnabled = on("vless", s.VLESSEnabled)
	s.RealityEnabled, s.RealityPort = on("reality", s.RealityEnabled), u.RealityPort
	s.HysteriaEnabled, s.HysteriaPort, s.HopStart, s.HopEnd = on("hysteria2", s.HysteriaEnabled), u.HysteriaPort, u.HopStart, u.HopEnd
	s.AWGEnabled, s.AWGPort = on("awg", s.AWGEnabled), awgPort
	return &s
}

// pickFreePort picks a port from the pick range free on network here.
func pickFreePort(network string) int {
	span := int64(mieruPortMax - mieruPortMin)
	for range 20 {
		p := mieruPortMin + int(time.Now().UnixNano()%span)
		if portFree(network, p) {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	return mieruPortMin + int(time.Now().UnixNano()%span)
}

// claimMieruPort gives the master's lane, switched on without a port (the default), one
// free here and held by nothing else of the server's, and keeps it: the default for
// this server, or any free one when that is taken. set is updated in place.
func (m *Manager) claimMieruPort(set *model.Settings) {
	transport := model.MieruTransportOr(set.MieruTransport)
	network := mieruNetwork(transport)
	custom, _ := m.store.Inbounds(model.LocalNodeID)
	held := mieruHeldOn(set, custom)
	port := clearOf(held, transport, defaultMieruPort(set.RealityPublicKey))
	if !portFree(network, port) {
		port = clearOf(held, transport, pickFreePort(network))
	}
	kept, err := m.store.ClaimMieruPort(port)
	if err != nil {
		logErr("mieru: could not keep the picked port", "err", err)
		return
	}
	set.MieruPort, set.MieruTransport = kept, transport
	m.ensureLocalConnGuard() // the new public port joins the per-IP guard
}

// defaultMieruPort is a server's mieru port when none was chosen: a port in the pick
// range derived from something unique to the server, so installs and nodes land on
// different ones, and the same server always on the same one.
func defaultMieruPort(seed string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte("mieru:" + seed))
	return mieruPortMin + int(h.Sum32()%uint32(mieruPortMax-mieruPortMin))
}

// nodeMieruSeed is what a node's default mieru port derives from: its id and when it
// was created — nothing an edit or a key regeneration changes, since the port is not
// stored until an operator picks one.
func nodeMieruSeed(n *model.Node) string { return fmt.Sprintf("node:%d:%d", n.ID, n.CreatedAt) }

// RetryMieru starts the master's mieru listener when the last sync could not (its port
// taken, say), without waiting for the next change to sync again. Under applyMu, like
// the sync, so neither overwrites the other's outcome.
func (m *Manager) RetryMieru() {
	if m.mieru == nil {
		return
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if p := m.mieruErr.Load(); p == nil || *p == "" {
		return
	}
	msg := ""
	if err := m.mieru.Retry(); err != nil {
		msg = err.Error()
	} else if m.mieru.Listening().Port == 0 {
		return
	}
	m.mieruErr.Store(&msg)
}
