package core

import (
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/nodeapi"
)

// Two callers validating the SAME port on the same node must both get an answer.
// Keyed by (network, port) instead of per waiter, the second registration evicted the
// first's channel and the first's cancel then removed the second's — so neither heard
// back and both saves silently skipped the check.
func TestProbeRegistryServesConcurrentWaitersForOnePort(t *testing.T) {
	r := newProbeRegistry()
	p := nodeapi.PortProbe{Network: "tcp", Port: 9443}

	ch1, cancel1 := r.add(7, p)
	ch2, cancel2 := r.add(7, p)
	defer cancel2()

	// The node need only be asked once, however many callers are waiting.
	if got := r.wanted(7); len(got) != 1 || got[0] != p {
		t.Fatalf("wanted = %v, want one entry for %v", got, p)
	}

	r.resolve(7, []nodeapi.PortProbeResult{{Network: "tcp", Port: 9443, Free: true}})
	for i, ch := range []<-chan nodeapi.PortProbeResult{ch1, ch2} {
		select {
		case res := <-ch:
			if !res.Free {
				t.Errorf("waiter %d got Free=false", i)
			}
		default:
			t.Errorf("waiter %d never heard back", i)
		}
	}

	// One caller giving up must not take the other's registration with it.
	cancel1()
	if got := r.wanted(7); len(got) != 1 {
		t.Fatalf("after one cancel, wanted = %v, want the survivor still pending", got)
	}
}

// The sync handler cuts a node's long-poll short so a pending request travels at
// once. That must be true only for a request it has NOT sent yet: an agent too old to
// answer never clears the entry, and without the distinction every one of its polls
// would return instantly — a hot request loop against the panel for the whole timeout.
func TestFreshWorkIsOnlyUnsentWork(t *testing.T) {
	m := &Manager{probes: newProbeRegistry(), checks: newCheckRegistry()}

	_, cancel := m.probes.add(7, nodeapi.PortProbe{Network: "tcp", Port: 9443})
	defer cancel()
	if !m.NodeHasFreshWork(7) {
		t.Fatal("a newly registered probe should cut the poll short")
	}

	m.NodeProbePorts(7) // the handler sends it
	if m.NodeHasFreshWork(7) {
		t.Error("an already-sent probe must let the next poll be held")
	}

	// A second, different question is fresh again.
	_, cancel2 := m.checks.add(7, nodeapi.ConfigCheckRequest{ID: "x"})
	defer cancel2()
	if !m.NodeHasFreshWork(7) {
		t.Error("a newly registered config check should cut the poll short")
	}
	m.NodeConfigCheck(7)
	if m.NodeHasFreshWork(7) {
		t.Error("an already-sent config check must let the next poll be held")
	}
}

// A verdict for a superseded question must not satisfy the one still being waited on.
func TestConfigCheckMatchesOnID(t *testing.T) {
	r := newCheckRegistry()
	ch, cancel := r.add(7, nodeapi.ConfigCheckRequest{ID: "current"})
	defer cancel()

	r.resolve(7, nodeapi.ConfigCheckResult{ID: "stale", OK: true})
	select {
	case res := <-ch:
		t.Fatalf("a stale verdict was delivered: %+v", res)
	default:
	}

	r.resolve(7, nodeapi.ConfigCheckResult{ID: "current", OK: false, Err: "boom"})
	select {
	case res := <-ch:
		if res.OK || res.Err != "boom" {
			t.Errorf("verdict = %+v", res)
		}
	default:
		t.Error("the matching verdict never arrived")
	}
}
