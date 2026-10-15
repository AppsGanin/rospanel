package core

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestMTProtoListenFront(t *testing.T) {
	t.Parallel()
	base := model.Settings{MTProtoEnabled: true, MTProtoPort: 20000, VLESSPort: 443, RealityDest: "donor.example"}
	cases := []struct {
		name, host, sni, kept string
		domain, front         string
	}{
		{"own domain", "vpn.example.com", "", "", "vpn.example.com", "127.0.0.1:443"},
		{"IP host with an SNI name", "203.0.113.5", "cdn.example.net", "", "cdn.example.net", "127.0.0.1:443"},
		{"address only", "203.0.113.5", "", "", "203-0-113-5.sslip.io", "127.0.0.1:443"},
		{"kept donor after a domain came", "vpn.example.com", "", "donor.example", "donor.example", "donor.example:443"},
		{"kept own name after the host moved to an IP", "203.0.113.5", "", "vpn.example.com", "vpn.example.com", "vpn.example.com:443"},
	}
	for _, c := range cases {
		s := base
		s.Host, s.SNI, s.MTProtoDomain = c.host, c.sni, c.kept
		l := MTProtoListen(&s)
		if l.Domain != c.domain || l.Front != c.front {
			t.Errorf("%s: domain %q front %q, want %q %q", c.name, l.Domain, l.Front, c.domain, c.front)
		}
	}
}

// A node's domain is kept once its lane runs: its host changing later leaves every link
// the users added working.
func TestNodeMTProtoDomainIsKept(t *testing.T) {
	t.Parallel()
	m, _ := newNotifyManager(t)
	n, err := m.store.CreateNode("NL", "203.0.113.7", "")
	if err != nil {
		t.Fatal(err)
	}
	m.claimNodeMTProtoDomain(n)
	n, _ = m.store.GetNode(n.ID)
	const want = "203-0-113-7.sslip.io"
	if n.MTProtoDomain != want {
		t.Fatalf("kept %q, want the name of its address %q", n.MTProtoDomain, want)
	}
	// The host moving on keeps the domain the links carry.
	n.Host = "198.51.100.9"
	set, err := m.store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeSettings(set, n).MTProtoDomainOr(); got != want {
		t.Fatalf("after the host changed the node's links name %q, want %q", got, want)
	}
}
