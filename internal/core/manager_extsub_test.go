package core

import (
	"context"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

const (
	extLinkA = "vless://11111111-2222-3333-4444-555555555555@9.9.9.9:443?type=tcp&security=tls&sni=a.example#Partner%20A"
	extLinkB = "trojan://secret@8.8.8.8:443?security=tls&sni=b.example#Partner%20B"
)

// A pasted list is a source like a URL is, read in place: the servers appear at
// once, the group editor offers them under the master, and a re-read with one
// server gone drops it — together with any grant that named it.
func TestExternalSubscriptionFromAPastedList(t *testing.T) {
	m := nodeTestManager(t)
	ctx := context.Background()

	if _, _, err := m.CreateExtSubscription(ctx, "x", "not a subscription"); err == nil {
		t.Fatal("a payload with no links was accepted")
	}
	if _, _, err := m.CreateExtSubscription(ctx, strings.Repeat("n", model.MaxExtSubscriptionName+1), extLinkA); err == nil {
		t.Fatal("an overlong name was accepted")
	}
	sub, report, err := m.CreateExtSubscription(ctx, "", extLinkA+"\n"+extLinkB)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sub.Name != "import" || report.Total != 2 || report.Added != 2 || sub.ServerCount != 2 || sub.LastError != "" {
		t.Fatalf("create: sub %+v report %+v", sub, report)
	}
	servers := m.EnabledExtServers()
	if len(servers) != 2 || servers[0].Link != extLinkA || servers[1].Protocol != "trojan" {
		t.Fatalf("servers: %+v", servers)
	}

	targets, err := m.GroupTargets()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets[0].External) != 2 || targets[0].External[1].Token != model.ExtToken(servers[1].ID) {
		t.Fatalf("group targets: %+v", targets[0].External)
	}
	g, err := m.CreateGroup("vip", []string{model.ExtToken(servers[1].ID), "ext:nonsense", "junk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Grants) != 1 {
		t.Fatalf("grants sanitised to %v", g.Grants)
	}

	// The operator switches server B off; a switched-off server is not handed out
	// but keeps its row (and its grant) for when it comes back.
	if err := m.SetExtServerEnabled(servers[1].ID, false); err != nil {
		t.Fatal(err)
	}
	if got := m.EnabledExtServers(); len(got) != 1 || got[0].ID != servers[0].ID {
		t.Fatalf("after switching B off: %+v", got)
	}
	if err := m.SetExtServerEnabled(999, true); err == nil {
		t.Fatal("an unknown server switched")
	}

	// The whole source off: nothing is handed out, the rows stay.
	if err := m.SetExtSubscriptionEnabled(sub.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := m.EnabledExtServers(); len(got) != 0 {
		t.Fatalf("source off still hands out: %+v", got)
	}
	all, _ := m.ExtServers()
	if len(all) != 2 {
		t.Fatalf("rows lost: %+v", all)
	}

	// Deleting the source sweeps the grant with it.
	if err := m.DeleteExtSubscription(sub.ID); err != nil {
		t.Fatal(err)
	}
	groups, _ := m.Groups()
	if len(groups[0].Grants) != 0 {
		t.Fatalf("grant survived its server: %v", groups[0].Grants)
	}
	if err := m.DeleteExtSubscription(sub.ID); err != nil {
		t.Fatal("deleting twice must be quiet")
	}
	if list, _ := m.ExtSubscriptions(); len(list) != 0 {
		t.Fatalf("subscriptions left: %+v", list)
	}
}

// A source that cannot be read keeps its servers and records why.
func TestExternalSubscriptionKeepsServersWhenTheReadFails(t *testing.T) {
	m := nodeTestManager(t)
	ctx := context.Background()
	sub, _, err := m.CreateExtSubscription(ctx, "p", extLinkA)
	if err != nil {
		t.Fatal(err)
	}
	// Replace the stored source with a URL the SSRF gate refuses, then sync.
	if err := m.store.SetExtSubscriptionSource(sub.ID, "http://127.0.0.1/sub"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SyncExtSubscription(ctx, sub.ID); err == nil {
		t.Fatal("a refused URL synced")
	}
	after, _ := m.store.ExtSubscription(sub.ID)
	if after.LastError == "" || after.ServerCount != 1 {
		t.Fatalf("failed read must keep the count and record the error: %+v", after)
	}
	if got := m.EnabledExtServers(); len(got) != 1 {
		t.Fatalf("servers dropped on a failed read: %+v", got)
	}
}
