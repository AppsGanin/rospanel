package server

import (
	"fmt"
	"testing"
	"time"
)

// A locked-out IP must stay locked out even when an attacker floods the limiter
// with thousands of throwaway addresses. The sweep used to clear the whole IP map
// once it passed maxKeys, which handed the banned attacker a fresh attempt budget
// — spraying unique IPs was a way to un-ban yourself.
func TestLoginLimiterFloodDoesNotClearLockout(t *testing.T) {
	l := newLoginLimiter()

	const victim = "203.0.113.7"
	for i := 0; i < l.maxFails; i++ {
		l.fail(victim, "admin")
	}
	if !l.blocked(victim, "admin") {
		t.Fatal("attacker IP not locked out after maxFails")
	}

	// Flood well past maxKeys with unique, non-blocked addresses to force a sweep.
	for i := 0; i < l.maxKeys*2; i++ {
		l.fail(fmt.Sprintf("198.51.100.%d.%d", i/256, i%256), "")
	}

	if !l.blocked(victim, "admin") {
		t.Fatal("lockout was cleared by an unrelated IP flood — attacker regained a fresh budget")
	}
	if len(l.ips) > l.maxKeys {
		t.Fatalf("IP map unbounded after flood: %d entries (cap %d)", len(l.ips), l.maxKeys)
	}
}

// Even if every tracked IP is locked out, memory must stay bounded: the sweep
// evicts the lockouts closest to expiring rather than growing without limit.
func TestLoginLimiterBoundedWhenAllBlocked(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < l.maxKeys+500; i++ {
		ip := fmt.Sprintf("198.51.100.%d.%d", i/256, i%256)
		for j := 0; j < l.maxFails; j++ {
			l.fail(ip, "")
		}
	}
	if len(l.ips) > l.maxKeys {
		t.Fatalf("IP map grew past cap with all-blocked entries: %d (cap %d)", len(l.ips), l.maxKeys)
	}
}

// The same escape, on the limiter in front of the subscription endpoint and the /v1
// API. It wiped its whole map once it overflowed, so an address already over its
// limit could spray from throwaway addresses until the wipe, and come back with a
// fresh window. A throttled address must stay throttled through a flood.
func TestIPRateLimiterFloodDoesNotUnthrottle(t *testing.T) {
	l := newIPRateLimiter(3, time.Minute)
	l.maxKeys = 64

	const attacker = "203.0.113.7"
	for i := range 3 {
		if !l.allow(attacker) {
			t.Fatalf("request %d of 3 refused", i+1)
		}
	}
	if l.allow(attacker) {
		t.Fatal("the fourth request inside the window was allowed — the limit is not in force")
	}

	// Far more distinct addresses than the map holds, each allowed once, as a spray is.
	for i := range 10 * l.maxKeys {
		l.allow(fmt.Sprintf("2001:db8::%x", i))
	}

	if l.allow(attacker) {
		t.Error("a flood of throwaway addresses reset the throttled address's window")
	}
	if n := len(l.hits); n > l.maxKeys {
		t.Errorf("the map holds %d entries past its cap of %d", n, l.maxKeys)
	}
}

// Bounded even in the worst case: every entry throttled, so nothing is cheap to shed.
func TestIPRateLimiterBoundedWhenAllThrottled(t *testing.T) {
	l := newIPRateLimiter(1, time.Minute)
	l.maxKeys = 32
	for i := range 20 * l.maxKeys {
		ip := fmt.Sprintf("198.51.100.%d-%d", i/250, i%250)
		l.allow(ip) // allowed once, and now at its limit of 1
		l.allow(ip) // throttled
	}
	if n := len(l.hits); n > l.maxKeys {
		t.Errorf("with every address throttled the map still grew to %d, past its cap of %d", n, l.maxKeys)
	}
}
