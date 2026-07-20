package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func openNodeStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestNodeCreateAndJoin(t *testing.T) {
	st := openNodeStore(t)

	n, err := st.CreateNode("NL #1", "nl1.example.com", "nginx")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.RawJoinToken == "" {
		t.Fatal("expected a raw join token")
	}
	if n.RealityPublicKey == "" || n.RealityPrivateKey == "" {
		t.Fatal("expected a generated REALITY keypair")
	}
	if n.DecoyTemplate != "nginx" {
		t.Fatalf("decoy = %q, want nginx", n.DecoyTemplate)
	}

	// A fresh node is not joined and has no permanent token yet.
	if got, _ := st.LookupNodeByToken("rpn_bogus"); got != nil {
		t.Fatal("bogus token resolved to a node")
	}

	// Consume the join token → permanent token; join token is now spent.
	joined, perm, err := st.ConsumeJoinToken(n.RawJoinToken)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if joined == nil || perm == "" {
		t.Fatalf("consume returned (%v, %q)", joined, perm)
	}
	// Reusing the join token fails (single-use).
	if again, _, _ := st.ConsumeJoinToken(n.RawJoinToken); again != nil {
		t.Fatal("join token was reusable")
	}
	// The permanent token resolves to the node.
	byTok, err := st.LookupNodeByToken(perm)
	if err != nil || byTok == nil || byTok.ID != n.ID {
		t.Fatalf("lookup by perm token = (%v, %v), want node %d", byTok, err, n.ID)
	}
}

// TestNodeNameUniqueIndex locks in the DB-level unique-name backstop (migration 0035):
// a case-insensitive duplicate is rejected even if the app-level check is bypassed, and
// a name is free to reuse once its node is deleted.
func TestNodeNameUniqueIndex(t *testing.T) {
	st := openNodeStore(t)
	n1, err := st.CreateNode("Dup", "a.example.com", "")
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	// A case-insensitive duplicate create is rejected at the DB level.
	if _, err := st.CreateNode("dup", "b.example.com", ""); !errors.Is(err, ErrNodeNameTaken) {
		t.Fatalf("duplicate create should return ErrNodeNameTaken, got %v", err)
	}
	// Renaming a different node onto the taken name is rejected too.
	n2, err := st.CreateNode("Other", "o.example.com", "")
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if err := st.UpdateNode(n2.ID, NodeEdit{Name: "DUP", Host: "o.example.com"}); !errors.Is(err, ErrNodeNameTaken) {
		t.Fatalf("rename collision should return ErrNodeNameTaken, got %v", err)
	}
	// After deleting the original, the name is free again (partial index over live rows).
	if err := st.DeleteNode(n1.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.CreateNode("Dup", "a2.example.com", ""); err != nil {
		t.Fatalf("name should be reusable after delete: %v", err)
	}
}

// TestIssueJoinTokenKeepsPermanent locks in the SSH re-provision fix: issuing a fresh
// join token WITHOUT revoke leaves the live node's permanent token working (so a failed
// install can't down it), while RegenJoinToken revokes it.
func TestIssueJoinTokenKeepsPermanent(t *testing.T) {
	st := openNodeStore(t)
	n, err := st.CreateNode("NL", "nl.example.com", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, perm, err := st.ConsumeJoinToken(n.RawJoinToken)
	if err != nil || perm == "" {
		t.Fatalf("consume: %v %q", err, perm)
	}

	// IssueJoinToken (provision) must NOT revoke the permanent token.
	if _, err := st.IssueJoinToken(n.ID); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if got, _ := st.LookupNodeByToken(perm); got == nil {
		t.Fatal("IssueJoinToken revoked the permanent token — a failed re-provision would down the node")
	}

	// RegenJoinToken (manual/security) MUST revoke it.
	if _, err := st.RegenJoinToken(n.ID); err != nil {
		t.Fatalf("regen: %v", err)
	}
	if got, _ := st.LookupNodeByToken(perm); got != nil {
		t.Fatal("RegenJoinToken should have revoked the old permanent token")
	}
}

func TestNodeJoinTokenExpiry(t *testing.T) {
	st := openNodeStore(t)
	n, err := st.CreateNode("expiring", "e.example.com", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Force the token to have already expired.
	if _, err := st.db.Exec(`UPDATE nodes SET join_expires_at = 1 WHERE id = ?`, n.ID); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if joined, _, _ := st.ConsumeJoinToken(n.RawJoinToken); joined != nil {
		t.Fatal("expired join token was accepted")
	}
	// RegenJoinToken issues a fresh, valid one.
	fresh, err := st.RegenJoinToken(n.ID)
	if err != nil {
		t.Fatalf("regen: %v", err)
	}
	if joined, _, _ := st.ConsumeJoinToken(fresh); joined == nil {
		t.Fatal("regenerated join token was rejected")
	}
}

func TestNodeEditAndOverrides(t *testing.T) {
	st := openNodeStore(t)
	n, _ := st.CreateNode("edit", "e.example.com", "nginx")

	yes := true
	dns := "1.1.1.1"
	rc := &model.RoutingConfig{BlockAds: true, BlockDomains: []string{"bad.example"}}
	if err := st.UpdateNode(n.ID, NodeEdit{
		Name: "edited", Host: "e2.example.com", DecoyTemplate: "10gag",
		Hysteria: &yes, Routing: rc, XrayDNS: &dns,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := st.GetNode(n.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "edited" || got.Host != "e2.example.com" || got.DecoyTemplate != "10gag" {
		t.Fatalf("edit not persisted: %+v", got)
	}
	if got.HysteriaEnabled == nil || !*got.HysteriaEnabled {
		t.Fatal("hysteria override not persisted")
	}
	if got.VLESSEnabled != nil {
		t.Fatal("vless override should stay nil (inherit)")
	}
	if got.XrayDNS == nil || *got.XrayDNS != "1.1.1.1" {
		t.Fatalf("dns override = %v", got.XrayDNS)
	}
	if got.Routing == nil || !got.Routing.BlockAds || len(got.Routing.BlockDomains) != 1 {
		t.Fatalf("routing override not persisted: %+v", got.Routing)
	}
}

func TestNodeStatusAndDelete(t *testing.T) {
	st := openNodeStore(t)
	n, _ := st.CreateNode("status", "s.example.com", "")

	if err := st.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen: 1000, NodeVersion: "1.2.3", XrayVersion: "v26.6.27",
		XrayRunning: true, CertSHA256: "abc", CertSelfSigned: false, ConfigHash: "h1",
	}); err != nil {
		t.Fatalf("status: %v", err)
	}
	got, _ := st.GetNode(n.ID)
	if got.LastSeen != 1000 || got.XrayVersion != "v26.6.27" || !got.XrayRunning {
		t.Fatalf("status not persisted: %+v", got)
	}
	if !got.Joined() {
		t.Fatal("node with a config hash should count as joined")
	}

	if err := st.DeleteNode(n.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if gone, _ := st.GetNode(n.ID); gone != nil {
		t.Fatal("node not deleted")
	}
}

func TestNodeTrafficDimension(t *testing.T) {
	st := openNodeStore(t)
	uid := seedUser(t, st)

	// Local (node 0) and a remote node accumulate independently.
	if err := st.AddDailyTraffic(uid, "2026-01-01", 100, 200); err != nil {
		t.Fatalf("local traffic: %v", err)
	}
	if err := st.AddDailyTrafficNode(uid, 7, "2026-01-01", 10, 20); err != nil {
		t.Fatalf("node traffic: %v", err)
	}
	// StatsSeries sums across nodes.
	pts, err := st.StatsSeries(uid, "2026-01-01", "2026-01-01")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(pts) != 1 || pts[0].Up != 110 || pts[0].Down != 220 {
		t.Fatalf("summed series = %+v, want up=110 down=220", pts)
	}
	// Per-node series isolates one node.
	local, _ := st.StatsSeriesNode(uid, 0, "2026-01-01", "2026-01-01")
	if len(local) != 1 || local[0].Up != 100 {
		t.Fatalf("local series = %+v, want up=100", local)
	}
	totals, _ := st.NodeTrafficTotals(0, "2026-01-01", "2026-01-01")
	if totals[0][0] != 100 || totals[7][0] != 10 {
		t.Fatalf("node totals = %+v", totals)
	}
	// Narrowed to one user, the same split must hold — this is what the user card's
	// per-server breakdown reads.
	mine, _ := st.NodeTrafficTotals(uid, "2026-01-01", "2026-01-01")
	if mine[0][0] != 100 || mine[7][0] != 10 {
		t.Fatalf("per-user node totals = %+v", mine)
	}
	if other, _ := st.NodeTrafficTotals(uid+999, "2026-01-01", "2026-01-01"); len(other) != 0 {
		t.Fatalf("a stranger's breakdown returned rows: %+v", other)
	}
}

// seedUser inserts a minimal user and returns its id, for FK-satisfying traffic rows.
func seedUser(t *testing.T, st *Store) int64 {
	t.Helper()
	u, err := st.CreateUser("tester", "uuid-1", "pass", "subtok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}
