package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/turnrelay"
)

// Users allowed on a WireGuard inbound are given their tunnel identity before the config
// is built from them — in a copy, since the slice handed in may be the snapshot every
// node shares — and the identity is the one AmneziaWG uses, stored once.
func TestClaimWireGuardGivesAllowedUsersAnIdentity(t *testing.T) {
	m := bulkTestManager(t)
	a := mkUser(t, m, "a", 0)
	b := mkUser(t, m, "b", 0)
	users, err := m.store.WorkingCredentials(1)
	if err != nil || len(users) != 2 {
		t.Fatalf("users: %v %v", users, err)
	}
	wg := model.Inbound{ID: 5, Protocol: model.InbWireGuard, Enabled: true}

	// No WireGuard inbound: nothing to claim, the same slice back.
	if out, claimed, ok := m.claimWireGuard(users, nil, nil); !ok || claimed || &out[0] != &users[0] {
		t.Fatal("claimed with no wireguard inbound")
	}

	// b may not use the inbound.
	access := map[int64]model.Access{b: {Tokens: map[string]bool{model.InboundToken(99): true}}}
	out, claimed, ok := m.claimWireGuard(users, []model.Inbound{wg}, access)
	if !ok || !claimed {
		t.Fatalf("claim: claimed=%v ok=%v", claimed, ok)
	}
	if users[0].WGPrivateKey != "" || users[1].WGPrivateKey != "" {
		t.Fatal("the users handed in were changed")
	}
	byID := map[int64]model.User{}
	for _, u := range out {
		byID[u.ID] = u
	}
	if byID[a].WGPrivateKey == "" || byID[a].AWGSlot == 0 {
		t.Errorf("a, allowed, has no identity: %+v", byID[a])
	}
	if byID[b].WGPrivateKey != "" {
		t.Error("b, not allowed, was given an identity")
	}
	stored, _ := m.store.GetUser(a)
	if stored.WGPrivateKey != byID[a].WGPrivateKey || stored.AWGSlot != byID[a].AWGSlot {
		t.Error("the identity was not stored")
	}

	// Once everyone allowed has one, nothing more is claimed.
	fresh, _ := m.store.WorkingCredentials(1)
	if _, claimed, ok := m.claimWireGuard(fresh, []model.Inbound{wg}, access); !ok || claimed {
		t.Errorf("claimed again: claimed=%v ok=%v", claimed, ok)
	}
}

// A relay runs for each enabled WireGuard inbound, on its public port, to its loopback
// listener — and a node is sent exactly that set.
func TestTurnRelaySpecs(t *testing.T) {
	custom := []model.Inbound{
		{ID: 1, Protocol: model.InbWireGuard, Enabled: true, Port: 56000, Opts: model.InboundOpts{WGLocalPort: 20001, TurnMaskKey: "k1"}},
		{ID: 2, Protocol: model.InbWireGuard, Enabled: false, Port: 56001, Opts: model.InboundOpts{WGLocalPort: 20002}},
		{ID: 3, Protocol: model.InbHysteria, Enabled: true, Port: 443},
		{ID: 4, Protocol: model.InbWireGuard, Enabled: true, Port: 56002}, // no local port yet
	}
	specs := turnRelaySpecs(custom)
	if len(specs) != 1 || specs[0] != (turnrelay.Spec{Port: 56000, Target: "127.0.0.1:20001", MaskKey: "k1"}) {
		t.Errorf("specs = %+v", specs)
	}
	if relays := nodeTurnRelays(custom); len(relays) != 1 || relays[0].Port != 56000 || relays[0].Target != "127.0.0.1:20001" || relays[0].MaskKey != "k1" {
		t.Errorf("node relays = %+v", relays)
	}
	// A manager built without a relay (as tests build them) syncs nothing and does not panic.
	m := &Manager{}
	m.syncTurnLocked(custom)
	m.StopTurn()
}

// The loopback port is kept clear of everything the server's other inbounds hold.
func TestAssignWGLocalPortAvoidsTakenPorts(t *testing.T) {
	m := bulkTestManager(t)
	for port := wgLocalPortMin; port < wgLocalPortMax-5; port++ {
		// Leave only a handful free, so a pick that ignored the stored inbounds would
		// almost surely land on a taken one.
		if _, err := m.store.CreateInbound(model.Inbound{
			ServerID: model.LocalNodeID, Name: "hy" + string(rune('a'+port%26)) + itoa(port), Protocol: model.InbHysteria,
			Port: port, Opts: model.InboundOpts{Transport: model.TrHysteria, Security: model.SecTLS},
		}); err != nil {
			t.Fatal(err)
		}
		if port-wgLocalPortMin > 60 {
			break
		}
	}
	in := model.Inbound{ServerID: model.LocalNodeID, Protocol: model.InbWireGuard, Port: 56000}
	for range 50 {
		in.Opts.WGLocalPort = 0
		if err := m.assignWGLocalPort(&in, 0); err != nil {
			t.Fatal(err)
		}
		if in.Opts.WGLocalPort < wgLocalPortMin || in.Opts.WGLocalPort > wgLocalPortMax {
			t.Fatalf("port %d out of range", in.Opts.WGLocalPort)
		}
		if in.Opts.WGLocalPort <= wgLocalPortMin+61 {
			t.Fatalf("port %d is held by a stored inbound", in.Opts.WGLocalPort)
		}
	}
	// A port already given is kept.
	in.Opts.WGLocalPort = 21000
	if err := m.assignWGLocalPort(&in, 0); err != nil || in.Opts.WGLocalPort != 21000 {
		t.Errorf("an assigned port was replaced: %d %v", in.Opts.WGLocalPort, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
