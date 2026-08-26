package core

import (
	"path/filepath"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
	"github.com/Shu1t3/rospanel-shu1t3/internal/store"
)

func proxyTestManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tgproxy.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Manager{store: st, reconcileCh: make(chan struct{}, 1)}, st
}

// A mode the panel does not know must be refused rather than silently treated as
// direct: the operator asked for a route, and quietly not taking it is the failure
// this whole setting exists to remove. The retired warp/opera modes are covered too —
// a stale browser tab can still post them.
func TestSaveTelegramProxyRejectsUnknownMode(t *testing.T) {
	m, _ := proxyTestManager(t)
	for _, mode := range []string{"wireguard", "warp", "opera"} {
		if err := m.SaveTelegramProxy(mode, ""); err == nil {
			t.Errorf("mode %q was accepted", mode)
		}
	}
}

// The custom mode needs an address, and a usable one.
func TestSaveTelegramProxyValidatesTheCustomURL(t *testing.T) {
	m, _ := proxyTestManager(t)
	for _, raw := range []string{"", "127.0.0.1:1080", "ftp://host:21"} {
		if err := m.SaveTelegramProxy(model.TGProxyCustom, raw); err == nil {
			t.Errorf("custom mode accepted %q", raw)
		}
	}
}

// Saving the Telegram route must never restart Xray. It changes nothing in the
// generated config — WARP's entrance exists whenever WARP does — and a reconcile here
// would drop every live VPN connection for a panel-side setting.
func TestSaveTelegramProxyNeverReconciles(t *testing.T) {
	m, st := proxyTestManager(t)
	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	set.WarpEnabled, set.WarpPrivateKey = true, "k"
	if err := st.SetWarp(set); err != nil {
		t.Fatalf("enable warp: %v", err)
	}

	// Including the WARP entrance itself: pointing Telegram at it is just a proxy URL.
	for _, raw := range []string{"socks5://10.0.0.1:1080", "socks5://127.0.0.1:18081"} {
		if err := m.SaveTelegramProxy(model.TGProxyCustom, raw); err != nil {
			t.Fatalf("save %q: %v", raw, err)
		}
	}
	if err := m.SaveTelegramProxy(model.TGProxyDirect, ""); err != nil {
		t.Fatalf("direct: %v", err)
	}
	select {
	case <-m.reconcileCh:
		t.Error("a Telegram proxy save queued a reconcile — that restarts Xray and drops every live connection")
	default:
	}
}

// The typed URL is stored whatever the mode, so switching to direct and back leaves
// the operator's address in the box rather than erasing it.
func TestSaveTelegramProxyKeepsTheURLAcrossDirect(t *testing.T) {
	m, st := proxyTestManager(t)
	if err := m.SaveTelegramProxy(model.TGProxyCustom, "socks5://10.0.0.1:1080"); err != nil {
		t.Fatalf("custom: %v", err)
	}
	if err := m.SaveTelegramProxy(model.TGProxyDirect, "socks5://10.0.0.1:1080"); err != nil {
		t.Fatalf("direct: %v", err)
	}
	got, err := st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if got.TelegramProxyURL() != "" {
		t.Errorf("direct mode still resolves to %q", got.TelegramProxyURL())
	}
	if got.TGProxy != "socks5://10.0.0.1:1080" {
		t.Errorf("the typed address was erased: %q", got.TGProxy)
	}
}
