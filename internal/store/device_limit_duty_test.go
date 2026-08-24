package store

import (
	"fmt"
	"testing"
	"time"
)

// simulateDuty runs `span` seconds in 5s steps — the real access-flush cadence — and
// reports how long the user spent in the generated config versus cut out of it.
//
// The important detail is that a cut user produces NO sightings: their client cannot
// connect, so nothing reaches the access log. That feedback loop is what decides the
// real behaviour of the device limit, and a simulation without it measures nothing.
func simulateDuty(t *testing.T, st *Store, id, start, span int64, live func(at int64) []string) (in, out int64) {
	t.Helper()
	inCfg := true
	for at := start; at < start+span; at += 5 {
		if inCfg {
			var hits []ConnectionHit
			for _, ip := range live(at) {
				hits = append(hits, ConnectionHit{UserID: id, IP: ip, SeenAt: at, Hits: 1})
			}
			if len(hits) > 0 {
				if err := st.AddConnections(hits); err != nil {
					t.Fatalf("connections: %v", err)
				}
			}
		}
		if err := st.StampDeviceOverLimit(at); err != nil {
			t.Fatalf("stamp: %v", err)
		}
		if inCfg = dcWorking(t, st, id, at); inCfg {
			in += 5
		} else {
			out += 5
		}
	}
	return in, out
}

// What DeviceLimitGrace costs and what it buys, measured rather than argued, over half
// an hour of simulated time. Both numbers are the point of the test: the first is the
// reason the grace exists, the second is the price, and a change that moves either one
// silently is exactly what this is here to catch.
func TestDeviceLimitDutyCycle(t *testing.T) {
	const span = 1800

	t.Run("a roaming device never loses a second", func(t *testing.T) {
		st := dcStore(t)
		now := time.Now().Unix()
		u := dcUser(t, st, "roam")
		// A new address every four minutes — a mobile carrier rotating inside its pool,
		// which is what the panel's own data shows real accounts doing.
		_, out := simulateDuty(t, st, u.ID, now, span, func(at int64) []string {
			return []string{fmt.Sprintf("10.2.0.%d", (at-now)/240)}
		})
		if out != 0 {
			t.Errorf("a single device changing address was cut for %ds", out)
		}
	})

	t.Run("two devices in continuous use are cut for a large share of the time", func(t *testing.T) {
		st := dcStore(t)
		now := time.Now().Unix()
		u := dcUser(t, st, "shared")
		in, out := simulateDuty(t, st, u.ID, now, span, func(int64) []string {
			return []string{"10.0.0.1", "10.0.0.2"}
		})
		t.Logf("in config %ds, cut %ds (%d%% available)", in, out, in*100/(in+out))
		// Not a permanent cut, and deliberately documented as such: once cut they stop
		// being seen, their addresses age out of the window, and they are readmitted.
		// The floor below is what stops that cycle being widened into no enforcement at
		// all without someone noticing.
		if out*100/(in+out) < 30 {
			t.Errorf("two addresses in continuous simultaneous use were cut only %d%% "+
				"of the time — the device limit has stopped meaning much", out*100/(in+out))
		}
	})
}
