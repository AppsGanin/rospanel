package awg

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/device"
)

func TestPeerUpdatesKeepUnchangedPeers(t *testing.T) {
	_, a, _ := GenerateKey()
	_, b, _ := GenerateKey()
	one := Peer{PublicKey: a, Addr: netip.MustParseAddr("10.66.0.2"), Email: "u1"}
	two := Peer{PublicKey: b, Addr: netip.MustParseAddr("10.66.0.3"), Email: "u2"}
	rename := one
	rename.Email = "renamed"
	if got, err := peerUpdates([]Peer{one, two}, []Peer{two, rename}); err != nil || got != "" {
		t.Fatalf("unchanged wire identities: %q, %v", got, err)
	}
	got, err := peerUpdates([]Peer{one, two}, []Peer{one})
	if err != nil {
		t.Fatal(err)
	}
	bHex, _ := keyHex(b)
	if got != "public_key="+bHex+"\nremove=true\n" {
		t.Fatalf("removing one peer touched other peers: %q", got)
	}
	moved := one
	moved.Addr = netip.MustParseAddr("10.66.0.4")
	got, err = peerUpdates([]Peer{one, two}, []Peer{moved, two})
	if err != nil {
		t.Fatal(err)
	}
	aHex, _ := keyHex(a)
	if got != "public_key="+aHex+"\nreplace_allowed_ips=true\nallowed_ip=10.66.0.4/32\n" {
		t.Fatalf("address change touched other peers or retained old allowed IPs: %q", got)
	}
}

func TestParamsWireEqual(t *testing.T) {
	p := RandomParams()
	renamed := p
	renamed.Imitation = "display name only"
	if !p.wireEqual(renamed) {
		t.Fatal("display metadata would restart the device")
	}
	changed := p
	changed.S4++
	if p.wireEqual(changed) {
		t.Fatal("wire padding change would be ignored")
	}
}

func TestPeerUpdatesPreserveLiveEndpointAndHandshake(t *testing.T) {
	handshakeWith(t, RandomParams(), func(srv *device.Device, cfg Config) {
		key := cfg.Peers[0].PublicKey
		var st PeerStat
		deadline := time.Now().Add(5 * time.Second)
		for {
			before, err := srv.IpcGet()
			if err != nil {
				t.Fatal(err)
			}
			st = ParseStats(before)[key]
			if st.Endpoint != "" && st.LastHandshake > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("server has no live peer")
			}
			time.Sleep(10 * time.Millisecond)
		}
		_, other, err := GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		next := append(append([]Peer(nil), cfg.Peers...), Peer{PublicKey: other, Addr: netip.MustParseAddr("10.66.0.99")})
		for _, pair := range [][2][]Peer{{cfg.Peers, next}, {next, cfg.Peers}} {
			update, err := peerUpdates(pair[0], pair[1])
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(update, "replace_peers") {
				t.Fatal("a live update would reset all peers")
			}
			if err := srv.IpcSet(update); err != nil {
				t.Fatal(err)
			}
			dump, err := srv.IpcGet()
			if err != nil {
				t.Fatal(err)
			}
			got := ParseStats(dump)[key]
			if got.Endpoint != st.Endpoint || got.LastHandshake != st.LastHandshake {
				t.Fatalf("unrelated peer update reset the active peer: before=%+v after=%+v", st, got)
			}
		}
	})
}

func TestDefaultMTUBudgetsForAWG31Padding(t *testing.T) {
	for range 100 {
		p := RandomParams()
		// IPv6 + UDP + WireGuard + AWG's two transport padding fields.
		wireSize := DefaultMTU + 40 + 8 + 32 + p.S4 + int(p.Padding.Max)
		if wireSize > 1500 {
			t.Fatalf("default AWG packet can exceed a 1500-byte path: %d", wireSize)
		}
	}
}
