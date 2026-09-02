package core

import (
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestConnPolicyDecide(t *testing.T) {
	allow := model.ConnPolicy{Mode: model.ConnPolicyAllow, Countries: []string{"RU", "BY"}}
	block := model.ConnPolicy{Mode: model.ConnPolicyBlock, Countries: []string{"NL"}}
	asn := model.ConnPolicy{Mode: model.ConnPolicyOff, ASNs: []uint32{16509}}

	cases := []struct {
		name    string
		p       model.ConnPolicy
		cc      string
		asn     uint32
		refused bool
		reason  string
	}{
		{"allow: listed", allow, "RU", 0, false, ""},
		{"allow: elsewhere", allow, "NL", 0, true, model.PolicyReasonCountry},
		{"allow: unknown country is never refused", allow, "", 0, false, ""},
		{"block: listed", block, "NL", 0, true, model.PolicyReasonCountry},
		{"block: elsewhere", block, "RU", 0, false, ""},
		{"asn: refused whatever the country", asn, "RU", 16509, true, model.PolicyReasonASN},
		{"asn: another network", asn, "RU", 1299, false, ""},
		{"asn beats the country rule", model.ConnPolicy{Mode: model.ConnPolicyAllow, Countries: []string{"RU"}, ASNs: []uint32{16509}},
			"RU", 16509, true, model.PolicyReasonASN},
		{"unknown ASN (0) matches nothing", model.ConnPolicy{ASNs: []uint32{16509}}, "RU", 0, false, ""},
	}
	for _, c := range cases {
		v := c.p.Decide(c.cc, c.asn, "org")
		if v.Refused != c.refused || v.Reason != c.reason {
			t.Errorf("%s: refused=%v reason=%q, want %v/%q", c.name, v.Refused, v.Reason, c.refused, c.reason)
		}
	}
}

func TestConnPolicyValidateAndNormalize(t *testing.T) {
	p := model.ConnPolicy{Mode: "allow", Countries: []string{" ru ", "RU", "by"}, ASNs: []uint32{7, 7, 0, 3}}
	n := p.Normalized()
	if strings.Join(n.Countries, ",") != "BY,RU" {
		t.Errorf("countries: %v", n.Countries)
	}
	if len(n.ASNs) != 2 || n.ASNs[0] != 3 || n.ASNs[1] != 7 {
		t.Errorf("asns: %v", n.ASNs)
	}
	if err := n.Validate(); err != nil {
		t.Errorf("a normal policy was refused: %v", err)
	}
	// An allow-list with nothing in it would cut every user off — refuse the save.
	if err := (model.ConnPolicy{Mode: "allow"}).Validate(); err == nil {
		t.Error("an empty allow-list was accepted")
	}
	if err := (model.ConnPolicy{Mode: "allow", Countries: []string{"xyz"}}).Validate(); err == nil {
		t.Error("a malformed country was accepted")
	}
	if err := (model.ConnPolicy{Mode: "off", BlockHours: -1}).Validate(); err == nil {
		t.Error("a negative block length was accepted")
	}
	// Off with no lists refuses nothing.
	if model.DefaultConnPolicy().Active() {
		t.Error("the default policy is active")
	}
	if !(model.ConnPolicy{ASNs: []uint32{1}}).Active() {
		t.Error("an ASN list alone should be active")
	}
}

// The policy is checked on every sighting, records a refusal against the user, and
// only writes a block when the operator asked it to enforce.
func TestConnPolicyRecordsAndBlocks(t *testing.T) {
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "abroad", 0, 0)

	// A policy the panel cannot resolve a country for refuses nobody: the test box
	// has no geo tables, so this is also what the check does on a fresh install.
	if err := m.SaveConnPolicy(model.ConnPolicy{Mode: model.ConnPolicyAllow, Countries: []string{"RU"}}); err != nil {
		t.Fatal(err)
	}
	m.CheckConnPolicy(u.ID, "198.51.100.7")
	if got, _ := m.BlockedIPs(10); len(got) != 0 {
		t.Fatalf("an address with no known country must not be blocked: %+v", got)
	}

	// With the verdict forced (the geo tables are what the check consults; here the
	// policy is applied directly), a refusal is recorded and, when enforcing, stored.
	p := model.ConnPolicy{Mode: model.ConnPolicyBlock, Countries: []string{"NL"}, Enforce: true, BlockHours: 2}
	if err := m.SaveConnPolicy(p); err != nil {
		t.Fatal(err)
	}
	v := p.Decide("NL", 0, "")
	if !v.Refused {
		t.Fatal("the fixture policy should refuse NL")
	}
	m.applyPolicyVerdict(u.ID, "203.0.113.9", v, p)

	blocked, err := m.BlockedIPs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0].IP != "203.0.113.9" || blocked[0].Reason != model.PolicyReasonCountry {
		t.Fatalf("block: %+v", blocked)
	}
	if until := blocked[0].Until - blocked[0].At; until < int64(time.Hour.Seconds()) || until > int64(3*time.Hour.Seconds()) {
		t.Errorf("block length %ds, want about two hours", until)
	}
	// The user's journal says why they were refused, without the address (their
	// connection list already carries that).
	found := false
	for _, e := range trail(t, m, u.ID) {
		if e.Action == model.EventPolicyRefused {
			found = true
			if d := fmtDetails(e.Details); !strings.Contains(d, "nl") || strings.Contains(d, "203.0.113.9") {
				t.Errorf("journal row: %v", e.Details)
			}
		}
	}
	if !found {
		t.Error("no journal row for the refusal")
	}

	// The nodes are handed the same list.
	list, err := m.store.BlockedIPList()
	if err != nil || len(list) != 1 || list[0] != "203.0.113.9" {
		t.Errorf("node list: %v %v", list, err)
	}

	// One address, one verdict per hour: a refused client retries constantly.
	before, _ := m.BlockedIPs(10)
	m.applyPolicyVerdict(u.ID, "203.0.113.9", v, p)
	if !m.policyActOnce("198.51.100.250") {
		t.Error("a fresh address should get a verdict")
	}
	if m.policyActOnce("198.51.100.250") {
		t.Error("the same address got a second verdict within the hour")
	}
	if after, _ := m.BlockedIPs(10); len(after) != len(before) {
		t.Errorf("a repeat refusal added a row: %d → %d", len(before), len(after))
	}

	// Lifting a block by hand removes it everywhere and re-arms the verdict.
	gone, err := m.UnblockIP("203.0.113.9")
	if err != nil || !gone {
		t.Fatalf("unblock: %v %v", gone, err)
	}
	if got, _ := m.BlockedIPs(10); len(got) != 0 {
		t.Errorf("still blocked: %+v", got)
	}
	if gone, _ := m.UnblockIP("203.0.113.9"); gone {
		t.Error("unblocking twice reported a second removal")
	}

	// Switching the policy off lets everyone it cut back in.
	m.applyPolicyVerdict(u.ID, "203.0.113.10", v, p)
	if got, _ := m.BlockedIPs(10); len(got) != 1 {
		t.Fatalf("expected one block before switching off: %+v", got)
	}
	if err := m.SaveConnPolicy(model.DefaultConnPolicy()); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.BlockedIPs(10); len(got) != 0 {
		t.Errorf("switching the policy off left blocks in place: %+v", got)
	}
}

// A lapsed block leaves the table, so what the nodes are handed matches what the
// kernel expires on its own.
func TestPolicyBlocksExpire(t *testing.T) {
	m := bulkTestManager(t)
	now := time.Now().Unix()
	for ip, until := range map[string]int64{"203.0.113.1": now + 3600, "203.0.113.2": now - 1} {
		if err := m.store.BlockIP(model.BlockedIP{IP: ip, Reason: model.PolicyReasonCountry, At: now - 10, Until: until}); err != nil {
			t.Fatal(err)
		}
	}
	if list, _ := m.store.BlockedIPList(); len(list) != 1 || list[0] != "203.0.113.1" {
		t.Errorf("a lapsed block is still handed out: %v", list)
	}
	m.PurgePolicyBlocks()
	if got, _ := m.BlockedIPs(10); len(got) != 1 {
		t.Errorf("purge: %+v", got)
	}
}
