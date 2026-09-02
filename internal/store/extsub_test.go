package store

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func extStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "ext.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func extServer(sub int64, key, name string) model.ExtServer {
	return model.ExtServer{SubID: sub, Key: key, Name: name, Protocol: "vless", Host: "h" + key, Port: 443, Link: "vless://" + key + "@h:443#" + name}
}

func TestExtServersReconcileAndKeepTheOperatorsChoice(t *testing.T) {
	st := extStore(t)
	id, err := st.CreateExtSubscription("partner", "https://example.com/sub")
	if err != nil {
		t.Fatal(err)
	}
	added, updated, removed, err := st.ReplaceExtServers(id, []model.ExtServer{extServer(id, "a", "A"), extServer(id, "b", "B")}, 100)
	if err != nil || added != 2 || updated != 0 || removed != 0 {
		t.Fatalf("first read: %d %d %d %v", added, updated, removed, err)
	}
	servers, _ := st.ExtServers()
	if len(servers) != 2 || servers[0].Link != "vless://a@h:443#A" {
		t.Fatalf("servers after first read: %+v", servers)
	}
	// The operator switches one off; the next read renames it and drops the other.
	if ok, err := st.SetExtServerEnabled(servers[0].ID, false); err != nil || !ok {
		t.Fatal(err, ok)
	}
	if ok, _ := st.SetExtServerEnabled(999, false); ok {
		t.Fatal("an unknown server reported as switched")
	}
	added, updated, removed, err = st.ReplaceExtServers(id, []model.ExtServer{extServer(id, "a", "A renamed"), extServer(id, "c", "C")}, 200)
	if err != nil || added != 1 || updated != 1 || removed != 1 {
		t.Fatalf("second read: %d %d %d %v", added, updated, removed, err)
	}
	servers, _ = st.ExtServers()
	if len(servers) != 2 {
		t.Fatalf("servers after second read: %+v", servers)
	}
	byKey := map[string]model.ExtServer{}
	for _, s := range servers {
		byKey[s.Key] = s
	}
	if a := byKey["a"]; a.Enabled || a.Name != "A renamed" || a.SeenAt != 200 {
		t.Fatalf("the renamed server lost its state: %+v", a)
	}
	if _, gone := byKey["b"]; gone {
		t.Fatal("a server no longer listed was kept")
	}
	// Only switched-on servers of switched-on sources are handed out.
	enabled, _ := st.EnabledExtServers()
	if len(enabled) != 1 || enabled[0].Key != "c" {
		t.Fatalf("enabled: %+v", enabled)
	}
	if err := st.SetExtSubscriptionEnabled(id, false); err != nil {
		t.Fatal(err)
	}
	if enabled, _ = st.EnabledExtServers(); len(enabled) != 0 {
		t.Fatal("a switched-off source still hands out servers")
	}
	if err := st.MarkExtSubscriptionSync(id, 2, "", 300); err != nil {
		t.Fatal(err)
	}
	sub, _ := st.ExtSubscription(id)
	if sub == nil || sub.Enabled || sub.LastOKAt != 300 || sub.ServerCount != 2 || sub.Source != "https://example.com/sub" {
		t.Fatalf("subscription: %+v", sub)
	}
	if err := st.MarkExtSubscriptionSync(id, 0, "boom", 400); err != nil {
		t.Fatal(err)
	}
	if sub, _ = st.ExtSubscription(id); sub.LastError != "boom" || sub.LastOKAt != 300 || sub.ServerCount != 2 {
		t.Fatalf("a failed read must keep the last good state: %+v", sub)
	}
}

func TestDeletingAnExternalSubscriptionSweepsItsGrants(t *testing.T) {
	st := extStore(t)
	id, _ := st.CreateExtSubscription("p", "https://example.com/sub")
	_, _, _, err := st.ReplaceExtServers(id, []model.ExtServer{extServer(id, "a", "A")}, 1)
	if err != nil {
		t.Fatal(err)
	}
	servers, _ := st.ExtServers()
	g, err := st.CreateGroup("vip", []string{model.ExtToken(servers[0].ID), model.BuiltinToken(0, model.LaneVLESS)})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteExtSubscription(id); err != nil {
		t.Fatal(err)
	}
	groups, _ := st.Groups()
	if len(groups) != 1 || len(groups[0].Grants) != 1 || groups[0].Grants[0] != model.BuiltinToken(0, model.LaneVLESS) {
		t.Fatalf("grants after delete: %+v (group %d)", groups, g.ID)
	}
	if servers, _ = st.ExtServers(); len(servers) != 0 {
		t.Fatal("servers outlived their source")
	}
	if sub, _ := st.ExtSubscription(id); sub != nil {
		t.Fatal("source still there")
	}
}
