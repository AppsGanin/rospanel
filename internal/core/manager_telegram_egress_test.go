package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// stubTelegramEgressProbe swaps the readiness probe for the duration of a test and
// reports how many times it ran.
func stubTelegramEgressProbe(t *testing.T, alive func(attempt int32) bool) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	prev := telegramEgressAlive
	telegramEgressAlive = func(context.Context, string) bool {
		return alive(calls.Add(1))
	}
	t.Cleanup(func() { telegramEgressAlive = prev })
	return &calls
}

// warpEgressManager points the Telegram proxy at the WARP entrance this panel runs —
// the state the startup wait exists for.
func warpEgressManager(t *testing.T) *Manager {
	t.Helper()
	m, st := proxyTestManager(t)
	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	set.WarpEnabled, set.WarpPrivateKey = true, "k"
	if err := st.SetWarp(set); err != nil {
		t.Fatalf("enable warp: %v", err)
	}
	set, err = st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if err := st.SetTelegramProxy(model.TGProxyCustom, set.WarpProxyURL()); err != nil {
		t.Fatalf("set proxy: %v", err)
	}
	return m
}

// A proxy the panel does not run must never hold up startup — and must not be probed
// at all. Someone else's proxy being down is their outage; blocking on it would turn
// that into a slow boot with no bots, which is strictly worse than starting and
// letting them retry.
func TestAwaitTelegramEgressSkipsForeignRoutes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
		raw  string
	}{
		{"direct", model.TGProxyDirect, ""},
		{"a proxy running elsewhere", model.TGProxyCustom, "socks5://10.0.0.1:1080"},
		{"loopback, but not an egress of ours", model.TGProxyCustom, "socks5://127.0.0.1:1080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st := proxyTestManager(t)
			calls := stubTelegramEgressProbe(t, func(int32) bool { return false })
			if err := st.SetTelegramProxy(tc.mode, tc.raw); err != nil {
				t.Fatalf("set proxy: %v", err)
			}
			start := time.Now()
			m.AwaitTelegramEgress(context.Background())
			if el := time.Since(start); el > time.Second {
				t.Errorf("waited %v for a route the panel does not run", el)
			}
			if n := calls.Load(); n != 0 {
				t.Errorf("probed a foreign route %d times", n)
			}
		})
	}
}

// The point of the wait: for an egress the panel brings up itself, keep probing until
// Telegram actually answers through it. Xray's inbound accepts from the moment the
// process starts, seconds before the tunnel behind it carries anything, so a single
// early check would let the bots start into a hole.
func TestAwaitTelegramEgressWaitsUntilTelegramAnswers(t *testing.T) {
	m := warpEgressManager(t)
	calls := stubTelegramEgressProbe(t, func(n int32) bool { return n >= 3 })

	start := time.Now()
	m.AwaitTelegramEgress(context.Background())
	elapsed := time.Since(start)

	if n := calls.Load(); n != 3 {
		t.Errorf("probed %d times, want 3 — it must retry rather than give up on the first miss", n)
	}
	if elapsed < time.Second {
		t.Errorf("returned after %v, so it never actually waited between probes", elapsed)
	}
	if elapsed > telegramEgressWait {
		t.Errorf("waited %v, past the %v budget", elapsed, telegramEgressWait)
	}
}

// An egress that never comes up must not hold the bots forever: they retry on their
// own, so this wait only removes the part we can predict.
func TestAwaitTelegramEgressGivesUpAtTheBudget(t *testing.T) {
	m := warpEgressManager(t)
	stubTelegramEgressProbe(t, func(int32) bool { return false })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	m.AwaitTelegramEgress(ctx)
	if el := time.Since(start); el > telegramEgressWait {
		t.Errorf("waited %v, past both the cancelled context and the %v budget", el, telegramEgressWait)
	}
}
