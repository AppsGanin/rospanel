package core

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// A ban refuses what it must not touch, each with its own reason: an address inside a
// tunnel or a private network does nothing at the firewall, the caller's own would lock
// them out, a trusted one is the operator's own exception, and a server's would cut a
// node off the panel.
func TestBanRefusals(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	servingNode(t, m, "n", "198.51.100.200")
	// A node known by name is refused by what the name resolves to.
	servingNode(t, m, "named", "node.example.com")
	m.hosts = newHostAddrs(func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "node.example.com" {
			return []netip.Addr{netip.MustParseAddr("198.51.100.201")}, nil
		}
		return nil, errors.New("no such host")
	})
	if err := m.SaveTrustedNets([]string{"192.0.2.0/24"}); err != nil {
		t.Fatal(err)
	}
	// Another admin's live session, last used from 198.51.100.210.
	adminID, err := m.store.CreateAdmin("colleague", "hash", model.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.CreateSessionFrom(adminID, time.Hour, "198.51.100.210", "ua"); err != nil {
		t.Fatal(err)
	}
	const admin = "203.0.113.50"
	for ip, code := range map[string]string{
		"not-an-ip":           "err.banBadIP",
		"2001:db8::1%eth0":    "err.banBadIP", // a zone nft cannot write, hiding the address from every check
		"198.51.100.201":      "err.banServer",
		"198.51.100.210":      "err.banAdmin",
		"10.66.0.8":           "err.banNotPublic", // a WireGuard / AmneziaWG peer
		"192.168.1.10":        "err.banNotPublic",
		"100.64.3.4":          "err.banNotPublic", // carrier-grade NAT
		"127.0.0.1":           "err.banNotPublic",
		"fe80::1":             "err.banNotPublic",
		"203.0.113.50":        "err.banSelf",
		"::ffff:203.0.113.50": "err.banSelf",
		"192.0.2.10":          "err.banTrusted",
		"198.51.100.200":      "err.banServer",
		"203.0.113.7":         "",
		"2001:db8:1::7":       "",
	} {
		err := m.CanBanIP(ip, admin)
		var ve *ValidationError
		switch {
		case code == "" && err != nil:
			t.Errorf("%s refused: %v", ip, err)
		case code != "" && (!errors.As(err, &ve) || ve.Code != code):
			t.Errorf("%s: %v, want %s", ip, err, code)
		}
	}
	if _, err := m.BanIP("10.66.0.8", 0, admin); err == nil {
		t.Error("a tunnel address was banned")
	}
	if ips, _ := m.store.BannedIPList(); len(ips) != 0 {
		t.Errorf("a refused ban was recorded: %v", ips)
	}
}

// A ban is recorded as the firewall writes the address, marks the user's address as
// banned, reaches every node, and lists with whose address it was; unbanning lifts it
// and the source policy's block on an address alike.
func TestBanAndUnban(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	n := servingNode(t, m, "n", "n.example.com")
	uid := mkUser(t, m, "owner-of-the-address", 0)
	now := time.Now().Unix()
	if err := m.store.AddConnections([]store.ConnectionHit{
		{UserID: uid, IP: "203.0.113.7", SeenAt: now, Hits: 3},
		{UserID: uid, IP: "203.0.113.8", SeenAt: now, Hits: 1},
	}); err != nil {
		t.Fatal(err)
	}

	ip, err := m.BanIP("::ffff:203.0.113.7", uid, "198.51.100.1")
	if err != nil || ip != "203.0.113.7" {
		t.Fatalf("ban: %q %v", ip, err)
	}
	conns, err := m.Connections(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conns {
		if c.Banned != (c.IP == "203.0.113.7") {
			t.Errorf("%s banned = %v", c.IP, c.Banned)
		}
		if c.ApproxSeconds != c.Count*accThrottle {
			t.Errorf("%s: approx %ds for %d sightings", c.IP, c.ApproxSeconds, c.Count)
		}
	}
	st, err := m.NodeStateChange(n, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.Meta.BannedIPs, []string{"203.0.113.7"}) {
		t.Errorf("node meta banned = %v", st.Meta.BannedIPs)
	}
	bans, err := m.Bans()
	if err != nil {
		t.Fatal(err)
	}
	if len(bans) != 1 || bans[0].Source != model.BanManual || bans[0].UserName != "owner-of-the-address" || bans[0].Until != 0 {
		t.Errorf("bans = %+v", bans)
	}

	// The source policy's block on another address is listed too, and lifted the same way.
	p := model.ConnPolicy{Mode: model.ConnPolicyBlock, Countries: []string{"NL"}, Enforce: true}
	m.applyPolicyVerdict(uid, "203.0.113.9", p.Decide("NL", 0, ""), p)
	if bans, _ = m.Bans(); len(bans) != 2 {
		t.Fatalf("bans with a policy block = %+v", bans)
	}
	for _, ip := range []string{"203.0.113.7", "203.0.113.9"} {
		if gone, err := m.UnbanIP(ip); err != nil || !gone {
			t.Errorf("unban %s: %v %v", ip, gone, err)
		}
	}
	if gone, _ := m.UnbanIP("203.0.113.7"); gone {
		t.Error("an address unbanned twice")
	}
	if bans, _ = m.Bans(); len(bans) != 0 {
		t.Errorf("bans left = %+v", bans)
	}
	m.notifyNodes()
	if st, _ = m.NodeStateChange(n, ""); len(st.Meta.BannedIPs) != 0 {
		t.Errorf("node still holds bans: %v", st.Meta.BannedIPs)
	}
}

// Trusting a network lifts a ban placed by hand inside it.
func TestTrustingLiftsABan(t *testing.T) {
	t.Parallel()
	m := nodeTestManager(t)
	if _, err := m.BanIP("203.0.113.7", 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveTrustedNets([]string{"203.0.113.0/24"}); err != nil {
		t.Fatal(err)
	}
	if ips, _ := m.store.BannedIPList(); len(ips) != 0 {
		t.Errorf("a trusted address stayed banned: %v", ips)
	}
}
