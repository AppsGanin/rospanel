package core

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// acc is the one-account list most of these cases need.
func acc(user, pass string) []model.SystemProxyAccount {
	return []model.SystemProxyAccount{{User: user, Pass: pass}}
}

func systemProxyManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "proxy.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Manager{store: st}, st
}

// The whole point of the feature: a server's proxy is ITS OWN. Enabling it on the
// master must not put a listener — or the master's password — on any node.
func TestSystemProxyIsPerServer(t *testing.T) {
	m, st := systemProxyManager(t)

	if err := m.SetSystemProxy(model.LocalNodeID, model.SystemProxy{
		SocksEnabled: true, SocksPort: 1080, Accounts: acc("master", "master-pass"),
	}); err != nil {
		t.Fatalf("master proxy: %v", err)
	}
	n, err := st.CreateNode("nl", "nl.example.com", "")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := m.SetSystemProxy(n.ID, model.SystemProxy{
		HTTPEnabled: true, HTTPPort: 8888, Accounts: acc("node", "node-pass"),
	}); err != nil {
		t.Fatalf("node proxy: %v", err)
	}

	set, _ := st.GetSettings()
	if !set.ProxySocksEnabled || set.ProxySocksPort != 1080 ||
		len(set.ProxyAccounts) != 1 || set.ProxyAccounts[0].User != "master" {
		t.Fatalf("master settings did not take the proxy: %+v", set.ProxySocksEnabled)
	}
	if set.ProxyHTTPEnabled {
		t.Error("the node's HTTP proxy leaked into the master's settings")
	}

	stored, err := st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if !stored.Proxy.HTTPEnabled || stored.Proxy.HTTPPort != 8888 ||
		len(stored.Proxy.Accounts) != 1 || stored.Proxy.Accounts[0].Pass != "node-pass" {
		t.Fatalf("node proxy not stored: %+v", stored.Proxy)
	}
	// The node's effective settings must carry ITS proxy and none of the master's.
	ns := nodeSettings(set, stored)
	if ns.ProxySocksEnabled || len(ns.ProxyAccounts) != 1 || ns.ProxyAccounts[0].User != "node" {
		t.Fatalf("the master's proxy leaked into the node's config: %+v", ns.ProxySocksEnabled)
	}
	if !ns.ProxyHTTPEnabled || ns.ProxyHTTPPort != 8888 {
		t.Fatalf("the node's own proxy is missing from its config: %+v", ns.ProxyHTTPEnabled)
	}
}

// An anonymous proxy on a public port becomes someone else's relay, so credentials
// are not optional — and a port already spoken for is refused before Xray is asked to
// bind it twice and fails to start at all.
func TestSystemProxyRefusesUnsafeConfigs(t *testing.T) {
	m, st := systemProxyManager(t)
	set, _ := st.GetSettings()

	cases := []struct {
		name string
		in   model.SystemProxy
	}{
		{"no accounts", model.SystemProxy{SocksEnabled: true, SocksPort: 1080}},
		{"no password", model.SystemProxy{
			SocksEnabled: true, SocksPort: 1080,
			Accounts: []model.SystemProxyAccount{{User: "u"}},
		}},
		{"duplicate logins", model.SystemProxy{
			SocksEnabled: true, SocksPort: 1080,
			Accounts: []model.SystemProxyAccount{{User: "u", Pass: "a"}, {User: "u", Pass: "b"}},
		}},
		{"port out of range", model.SystemProxy{SocksEnabled: true, SocksPort: 99999, Accounts: acc("u", "p")}},
		{"both on one port", model.SystemProxy{
			SocksEnabled: true, SocksPort: 1080, HTTPEnabled: true, HTTPPort: 1080, Accounts: acc("u", "p"),
		}},
		{"colon in the login", model.SystemProxy{SocksEnabled: true, SocksPort: 1080, Accounts: acc("a:b", "p")}},
		{"port held by a built-in lane", model.SystemProxy{
			SocksEnabled: true, SocksPort: set.VLESSPort, Accounts: acc("u", "p"),
		}},
		{"port held by Xray's own API", model.SystemProxy{
			SocksEnabled: true, SocksPort: xray.APIPort, Accounts: acc("u", "p"),
		}},
	}
	for _, c := range cases {
		if err := m.SetSystemProxy(model.LocalNodeID, c.in); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}

	// And nothing was written by any of them.
	after, _ := st.GetSettings()
	if after.ProxySocksEnabled || after.ProxyHTTPEnabled || len(after.ProxyAccounts) != 0 {
		t.Fatalf("a rejected configuration still landed: %+v", after.ProxyAccounts)
	}
}

// Enabling a protocol without a port is the common case from the API ("turn it on"),
// so it gets the documented default rather than a validation error.
func TestSystemProxyFillsDefaultPorts(t *testing.T) {
	m, st := systemProxyManager(t)
	if err := m.SetSystemProxy(model.LocalNodeID, model.SystemProxy{
		SocksEnabled: true, HTTPEnabled: true, Accounts: acc("u", "p"),
	}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	set, _ := st.GetSettings()
	if set.ProxySocksPort != model.SystemProxySocksPortDefault ||
		set.ProxyHTTPPort != model.SystemProxyHTTPPortDefault {
		t.Fatalf("ports = %d/%d, want the defaults", set.ProxySocksPort, set.ProxyHTTPPort)
	}
	// Re-saving the same thing must not report the proxy colliding with itself.
	if err := m.SetSystemProxy(model.LocalNodeID, model.SystemProxy{
		SocksEnabled: true, SocksPort: set.ProxySocksPort,
		HTTPEnabled: true, HTTPPort: set.ProxyHTTPPort, Accounts: acc("u", "p"),
	}); err != nil {
		t.Fatalf("re-save: %v", err)
	}
}

// A custom inbound must not be allowed onto a proxy's port either — the collision is
// symmetric, and only one of the two listeners would come up.
func TestSystemProxyPortsAreReserved(t *testing.T) {
	_, st := systemProxyManager(t)
	set, _ := st.GetSettings()
	set.ProxySocksPort, set.ProxyHTTPPort = 1080, 3128

	r := reservedPorts(set)
	for _, port := range []int{1080, 3128} {
		if _, held := r[port]; !held {
			t.Errorf("port %d is not reserved, so a custom inbound could claim it", port)
		}
	}
}
