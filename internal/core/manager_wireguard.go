package core

import (
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/sub"
	"github.com/AppsGanin/rospanel/internal/turnrelay"
)

// WireGuard inbounds behind a TURN relay (see model/inbound_wireguard.go). The inbound
// itself is Xray's; what is here is what Xray cannot do for it — the users' tunnel
// identities, the loopback port behind the relay, the relay, and the file a user
// imports.

// wgLocalPortMin and wgLocalPortMax bound the loopback port a WireGuard inbound's Xray
// listener is given: below Linux's ephemeral range (32768 up), where an outgoing socket
// could be holding the port at the moment Xray starts.
const (
	wgLocalPortMin = 20000
	wgLocalPortMax = 29999
)

// wireGuardInboundIDs lists the WireGuard inbounds among a server's custom ones.
func wireGuardInboundIDs(custom []model.Inbound) []int64 {
	var out []int64
	for _, in := range custom {
		if in.Protocol == model.InbWireGuard {
			out = append(out, in.ID)
		}
	}
	return out
}

// claimWireGuard gives every user allowed on one of a server's WireGuard inbounds the
// tunnel identity their peer is built from — the one they have for AmneziaWG, claimed the
// same way (claimAWG). users is not changed: when anyone needed a claim, a copy holding
// it is returned and claimed is true, because the users handed in may be the snapshot
// every node shares. ok is false when a claim failed; those users stay out of the
// inbound until one succeeds.
func (m *Manager) claimWireGuard(users []model.User, custom []model.Inbound, access map[int64]model.Access) (out []model.User, claimed, ok bool) {
	ids := wireGuardInboundIDs(custom)
	if len(ids) == 0 {
		return users, false, true
	}
	var need []int
	for i := range users {
		u := &users[i]
		if u.WGPrivateKey != "" && u.AWGSlot != 0 {
			continue
		}
		a := model.AccessOf(access, u.ID)
		for _, id := range ids {
			if a.AllowsInbound(id) {
				need = append(need, i)
				break
			}
		}
	}
	if len(need) == 0 {
		return users, false, true
	}
	out = append([]model.User(nil), users...)
	claim := make([]*model.User, 0, len(need))
	for _, i := range need {
		claim = append(claim, &out[i])
	}
	if err := m.claimAWG(claim); err != nil {
		logErr("wireguard: user tunnel identities", "err", err)
		return out, true, false
	}
	return out, true, true
}

// assignWGLocalPort gives a WireGuard inbound that has none the loopback port its Xray
// listener takes: one no built-in lane, no other inbound of the server and — on the
// master, where it can be asked — nothing on the box holds. It is kept for the life of
// the inbound (UpdateInbound carries it forward), so the relay's target never moves.
func (m *Manager) assignWGLocalPort(in *model.Inbound, excludeID int64) error {
	if in.Protocol != model.InbWireGuard || in.Opts.WGLocalPort != 0 {
		return nil
	}
	set, err := m.effectiveSettings(in.ServerID)
	if err != nil {
		return err
	}
	existing, err := m.store.Inbounds(in.ServerID)
	if err != nil {
		return err
	}
	reserved := reservedPorts(set)
	m.holdPanelPort(reserved)
	taken := map[int]bool{in.Port: true}
	for _, e := range existing {
		if e.ID == excludeID {
			continue
		}
		taken[e.Port] = true
		if e.Opts.WGLocalPort != 0 {
			taken[e.Opts.WGLocalPort] = true
		}
	}
	for range 200 {
		p := wgLocalPortMin + rand.IntN(wgLocalPortMax-wgLocalPortMin+1)
		if _, held := reserved.OnUDP(p); held || taken[p] {
			continue
		}
		if in.ServerID == model.LocalNodeID && !portFree("udp", p) {
			continue
		}
		in.Opts.WGLocalPort = p
		return nil
	}
	return fmt.Errorf("wireguard: no free loopback port in %d-%d", wgLocalPortMin, wgLocalPortMax)
}

// turnRelaySpecs is the relays a server's WireGuard inbounds need: one on each inbound's
// public port, forwarding to its loopback listener.
func turnRelaySpecs(custom []model.Inbound) []turnrelay.Spec {
	var out []turnrelay.Spec
	for _, in := range custom {
		if in.Protocol == model.InbWireGuard && in.Enabled && in.Opts.WGLocalPort > 0 {
			out = append(out, turnrelay.Spec{
				Port:    in.Port,
				Target:  net.JoinHostPort("127.0.0.1", strconv.Itoa(in.Opts.WGLocalPort)),
				MaskKey: in.Opts.TurnMaskKey,
			})
		}
	}
	return out
}

// nodeTurnRelays is turnRelaySpecs as a node's state carries it.
func nodeTurnRelays(custom []model.Inbound) []nodeapi.TurnRelay {
	var out []nodeapi.TurnRelay
	for _, s := range turnRelaySpecs(custom) {
		out = append(out, nodeapi.TurnRelay{Port: s.Port, Target: s.Target, MaskKey: s.MaskKey})
	}
	return out
}

// syncTurnLocked runs the master's relays for its WireGuard inbounds. Called with the
// inbounds a config was just generated from, so a relay never points at a listener the
// running Xray does not have. A relay that cannot start is logged and retried on the
// next apply; the rest of the server is not held up by it.
func (m *Manager) syncTurnLocked(custom []model.Inbound) {
	if err := m.turn.Sync(turnRelaySpecs(custom)); err != nil {
		logErr("turn relay", "err", err)
	}
}

// ClaimTunnelIdentity gives one user their tunnel key and address if they have none,
// recording them on u.
func (m *Manager) ClaimTunnelIdentity(u *model.User) error { return m.claimAWG([]*model.User{u}) }

// StopTurn stops the master's relays, at shutdown.
func (m *Manager) StopTurn() { m.turn.Close() }

// WireGuardClientConfig is the WireGuard config a user imports for one WireGuard
// inbound: the tunnel pointed at the local end of their TURN client, which carries it to
// the relay. Fails when the user cannot be a peer of it; a file the inbound will not
// accept is worse than none.
func (m *Manager) WireGuardClientConfig(u *model.User, s *model.Settings, in *model.Inbound) (string, error) {
	if in.Protocol != model.InbWireGuard || in.Opts.WGPublicKey == "" {
		return "", fmt.Errorf("wireguard: inbound %d is not a wireguard inbound", in.ID)
	}
	if err := m.claimAWG([]*model.User{u}); err != nil {
		return "", err
	}
	if _, ok := awg.ClientAddr(u.AWGSlot); !ok {
		return "", fmt.Errorf("wireguard: no address left on the tunnel subnet for user %d", u.ID)
	}
	if u.WGPrivateKey == "" {
		return "", fmt.Errorf("wireguard: stored key for user %d is unreadable", u.ID)
	}
	return sub.TurnClientConf(*u, s, *in), nil
}
