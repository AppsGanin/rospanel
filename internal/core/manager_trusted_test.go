package core

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A trusted network is one the three automatic bans leave alone. Each of them is
// driven here through the exact decision it makes — the firewall call itself is a
// no-op off Linux, so what is checked is whether the ban would have been placed.

func TestTrustedAddressIsNeverBannedForProxyPasswords(t *testing.T) {
	m := bulkTestManager(t)
	m.guard = newBruteGuard()
	if err := m.SaveTrustedNets([]string{"198.51.100.0/24"}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 * bruteMaxTries {
		if m.noteProxyAuthReject("198.51.100.20") {
			t.Fatalf("a trusted address earned a ban on failure %d", i+1)
		}
	}
	banned := false
	for range bruteMaxTries {
		banned = banned || m.noteProxyAuthReject("203.0.113.20")
	}
	if !banned {
		t.Error("an untrusted address was not banned after the threshold — the guard itself is off")
	}
}

func TestTrustedScannerIsRecordedButNotBlocked(t *testing.T) {
	m := bulkTestManager(t)
	if err := m.SetProbeBlock(true); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveTrustedNets([]string{"2001:db8::/32"}); err != nil {
		t.Fatal(err)
	}
	if m.probeBlockDue("2001:db8::7") {
		t.Error("a trusted scanner is due a firewall block")
	}
	if !m.probeBlockDue("203.0.113.7") {
		t.Error("an untrusted scanner is not due a block with auto-blocking on")
	}
}

func TestTrustedAddressIsRefusedOnRecordButNotBlocked(t *testing.T) {
	m := bulkTestManager(t)
	u, _ := m.CreateUser(adminCtx(), "office", 0, 0)
	p := model.ConnPolicy{Mode: model.ConnPolicyBlock, Countries: []string{"NL"}, Enforce: true}
	if err := m.SaveConnPolicy(p); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveTrustedNets([]string{"203.0.113.10"}); err != nil {
		t.Fatal(err)
	}
	m.applyPolicyVerdict(u.ID, "203.0.113.10", p.Decide("NL", 0, ""), p)
	if got, _ := m.BlockedIPs(10); len(got) != 0 {
		t.Fatalf("a trusted address was blocked: %+v", got)
	}
	found := false
	for _, e := range trail(t, m, u.ID) {
		if e.Action == model.EventPolicyRefused {
			found = true
			if d := fmtDetails(e.Details); !strings.Contains(d, "blocked:false") {
				t.Errorf("the journal says the trusted address was blocked: %v", e.Details)
			}
		}
	}
	if !found {
		t.Error("the refusal of a trusted address left no journal row")
	}
}

// Trusting a network lifts what is already banned inside it — an office adds itself
// because it was just cut off — and nothing outside it.
func TestTrustingANetworkLiftsItsBans(t *testing.T) {
	m := bulkTestManager(t)
	m.guard = newBruteGuard()
	p := model.ConnPolicy{Mode: model.ConnPolicyBlock, Countries: []string{"NL"}, Enforce: true}
	if err := m.SaveConnPolicy(p); err != nil {
		t.Fatal(err)
	}
	v := p.Decide("NL", 0, "")
	m.applyPolicyVerdict(0, "198.51.100.9", v, p)
	m.applyPolicyVerdict(0, "203.0.113.9", v, p)
	for range bruteMaxTries {
		m.noteProxyAuthReject("198.51.100.30")
	}

	if err := m.SaveTrustedNets([]string{"198.51.100.0/24"}); err != nil {
		t.Fatal(err)
	}
	blocked, _ := m.BlockedIPs(10)
	if len(blocked) != 1 || blocked[0].IP != "203.0.113.9" {
		t.Errorf("after trusting 198.51.100.0/24 the policy still blocks %+v — want only 203.0.113.9", blocked)
	}
	m.guard.mu.Lock()
	_, remembered := m.guard.banned["198.51.100.30"]
	m.guard.mu.Unlock()
	if remembered {
		t.Error("the brute-force guard still remembers a ban inside the trusted network")
	}
}

func TestTrustedNetsAreSavedNormalizedAndBadListsAreRefused(t *testing.T) {
	m := bulkTestManager(t)
	if err := m.SaveTrustedNets([]string{"198.51.100.7/24", " 203.0.113.1 "}); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveTrustedNets([]string{"203.0.113.2", "0.0.0.0/0"}); err == nil {
		t.Fatal("a list trusting the whole internet was accepted")
	}
	// Read back through the store, not the cache: the refused save must not have
	// reached either.
	stored, err := m.store.TrustedNets()
	if err != nil {
		t.Fatal(err)
	}
	want := "198.51.100.0/24,203.0.113.1/32"
	if strings.Join(stored, ",") != want || strings.Join(m.TrustedNets(), ",") != want {
		t.Errorf("stored %v, cached %v — want %s", stored, m.TrustedNets(), want)
	}
	if m.Trusted("203.0.113.2") {
		t.Error("an address from the refused list is trusted")
	}
}
