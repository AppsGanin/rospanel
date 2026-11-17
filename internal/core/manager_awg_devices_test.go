package core

import (
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// A device key is a peer of its own under the user's email, on its own address;
// the cap counts the main key; a hwid gets the same key back; the config written
// with a picked key carries it; deleting it takes the peer away; a UUID reset
// rekeys it in place.
func TestAWGDeviceKeys(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "a", 0, 0)
	set, _ := m.store.GetSettings()
	set.HWIDFallbackLimit = 3
	if err := m.store.SetHWIDSettings(set); err != nil {
		t.Fatal(err)
	}

	d1, made, err := m.AddUserAWGKey(ctx, u.ID, "  Ноутбук   дома ", "")
	if err != nil || !made || d1.Name != "Ноутбук дома" {
		t.Fatalf("add: %+v made=%v err=%v", d1, made, err)
	}
	k := m.AWGKeyForHWID(ctx, u.ID, "hw-1", "Pixel 9")
	if k == nil {
		t.Fatal("a client with an hwid got no key while there was room")
	}
	if again := m.AWGKeyForHWID(ctx, u.ID, "hw-1", "Pixel 9"); again == nil || again.ID != k.ID || again.Key != k.Key {
		t.Fatalf("the same hwid got another key: %+v vs %+v", again, k)
	}
	// Three keys now (main + 2) of 3: full. A new hwid falls back to the main key.
	if k2 := m.AWGKeyForHWID(ctx, u.ID, "hw-2", ""); k2 != nil {
		t.Fatalf("a key past the cap: %+v", k2)
	}
	if _, _, err := m.AddUserAWGKey(ctx, u.ID, "x", ""); err == nil {
		t.Fatal("a key by hand past the cap")
	}

	users, _ := m.store.WorkingCredentials(1)
	peers := m.awgPeers(model.LocalNodeID, users, nil)
	if len(peers) != 3 {
		t.Fatalf("peers %d, want the main key and two devices", len(peers))
	}
	seen := map[string]bool{}
	for _, p := range peers {
		if p.Email != model.UserEmail(u.ID) || seen[p.Addr.String()] || seen[p.PublicKey] {
			t.Fatalf("peers must share the email and nothing else: %+v", peers)
		}
		seen[p.Addr.String()], seen[p.PublicKey] = true, true
	}

	// The config written with a device key carries it and its address.
	fresh, _ := m.store.GetUser(u.ID)
	set.AWGEnabled, set.AWGPort, set.Host, set.ServerID = true, 40000, "vpn.example.com", model.LocalNodeID
	if err := m.ensureMasterAWGIdentity(set, false); err != nil {
		t.Fatal(err)
	}
	pick, _ := m.AWGKeyByID(u.ID, d1.ID)
	fresh.AWGPick = pick
	conf, err := m.AWGClientConfig(fresh, set)
	addr, _ := awg.ClientAddr(pick.Slot)
	if err != nil || !strings.Contains(conf, "PrivateKey = "+pick.Key) || !strings.Contains(conf, "Address = "+addr.String()+"/32") {
		t.Fatalf("config with a device key: %v\n%s", err, conf)
	}

	// The node rows carry the extra keys, and the node rebuilds them as peers.
	meta := nodeapi.NodeMeta{AWG: &nodeapi.AWGState{Port: 1, PrivateKey: "x"}}
	for _, p := range peers {
		meta.AWG.Peers = append(meta.AWG.Peers, nodeapi.AWGPeer{PublicKey: p.PublicKey, Addr: p.Addr.String(), Email: p.Email})
	}
	rows, _, err := splitRows(&xray.SplitConfig{}, users, meta, nil)
	if err != nil || len(rows) != 1 || !strings.Contains(string(rows[0]), `"awg_extra"`) {
		t.Fatalf("split rows: %v %s", err, rows)
	}

	// UUID reset: new key on the same address.
	if _, err := m.ResetUserUUID(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := m.AWGKeyByID(u.ID, d1.ID)
	if after.Key == pick.Key || after.Slot != pick.Slot {
		t.Errorf("reset: key changed %v, slot kept %v", after.Key != pick.Key, after.Slot == pick.Slot)
	}

	// Deleting a key drops its peer; unbinding the hwid drops the client's.
	if _, err := m.store.RegisterDevice(u.ID, model.Device{HWID: "hw-1", LastSeen: 1}, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteUserAWGKey(ctx, u.ID, d1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UnbindDevice(ctx, u.ID, "hw-1"); err != nil {
		t.Fatal(err)
	}
	users, _ = m.store.WorkingCredentials(1)
	if p := m.awgPeers(model.LocalNodeID, users, nil); len(p) != 1 {
		t.Fatalf("after delete and unbind: %d peers, want the main key alone", len(p))
	}
	if err := m.DeleteUserAWGKey(ctx, u.ID, d1.ID); err == nil {
		t.Error("deleting a key twice succeeded")
	}
}

// A device cap lowered after the keys were made (a cheaper plan) leaves the newest
// keys out of force: no peer, no config — and the sync notices without any key
// being touched.
func TestAWGDeviceKeysFollowALoweredCap(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "a", 0, 0)
	if err := m.store.SetUserHWIDLimit(u.ID, 3); err != nil {
		t.Fatal(err)
	}
	old, _, err := m.AddUserAWGKey(ctx, u.ID, "old", "")
	if err != nil {
		t.Fatal(err)
	}
	young, _, err := m.AddUserAWGKey(ctx, u.ID, "young", "")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := m.store.AWGKeysSignature()
	users, _ := m.store.WorkingCredentials(1)
	if p := m.awgPeers(model.LocalNodeID, users, nil); len(p) != 3 {
		t.Fatalf("%d peers under a cap of 3", len(p))
	}

	if err := m.store.SetUserHWIDLimit(u.ID, 2); err != nil {
		t.Fatal(err)
	}
	if after, _ := m.store.AWGKeysSignature(); after == before {
		t.Error("the signature did not change with the cap")
	}
	users, _ = m.store.WorkingCredentials(1)
	if p := m.awgPeers(model.LocalNodeID, users, nil); len(p) != 2 {
		t.Fatalf("%d peers under a cap of 2, want the main key and the oldest device", len(p))
	}
	if k, _ := m.AWGKeyByID(u.ID, young.ID); k != nil {
		t.Error("a key past the cap is handed out")
	}
	if k, _ := m.AWGKeyByID(u.ID, old.ID); k == nil {
		t.Error("the oldest key lost its place")
	}
	if _, _, err := m.AddUserAWGKey(ctx, u.ID, "more", ""); err == nil {
		t.Error("a key added past the cap")
	}
}

// The keys a user's subscription clients were given go with those clients: on
// unbinding them all, on rotating the link, on forgetting idle ones. Keys made by
// hand stay.
func TestHWIDKeysGoWithTheirClients(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	set, _ := m.store.GetSettings()
	set.HWIDFallbackLimit, set.HWIDTTLDays = 10, 30
	if err := m.store.SetHWIDSettings(set); err != nil {
		t.Fatal(err)
	}
	count := func(id int64) (hand, client int) {
		list, _ := m.store.ListAWGDevices(id)
		for _, d := range list {
			if d.HWID == "" {
				hand++
			} else {
				client++
			}
		}
		return
	}
	bind := func(id int64, hwid string, seen int64) {
		if _, err := m.store.RegisterDevice(id, model.Device{HWID: hwid, LastSeen: seen}, 0); err != nil {
			t.Fatal(err)
		}
		if m.AWGKeyForHWID(ctx, id, hwid, "") == nil {
			t.Fatalf("no key for %s", hwid)
		}
	}

	a, _ := m.CreateUser(ctx, "a", 0, 0)
	_, _, _ = m.AddUserAWGKey(ctx, a.ID, "hand", "")
	bind(a.ID, "a1", time.Now().Unix())
	bind(a.ID, "a2", time.Now().Unix())
	if _, err := m.UnbindAllDevices(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if h, c := count(a.ID); h != 1 || c != 0 {
		t.Errorf("unbind all: %d by hand, %d of clients; want 1, 0", h, c)
	}

	b, _ := m.CreateUser(ctx, "b", 0, 0)
	bind(b.ID, "b1", time.Now().Unix())
	if _, err := m.RotateSubToken(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, c := count(b.ID); c != 0 {
		t.Errorf("rotate: %d client keys left", c)
	}

	c, _ := m.CreateUser(ctx, "c", 0, 0)
	bind(c.ID, "idle", time.Now().AddDate(0, 0, -40).Unix())
	bind(c.ID, "fresh", time.Now().Unix())
	m.PurgeIdleDevices()
	list, _ := m.store.ListAWGDevices(c.ID)
	if len(list) != 1 || list[0].HWID != "fresh" {
		t.Errorf("idle purge left %+v, want the fresh client's key alone", list)
	}
}
