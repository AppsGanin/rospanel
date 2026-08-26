package store

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestShapedUsersPairsCapsWithAddresses(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "speed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	capped, err := st.CreateUser("capped", "u1", "pw", "t1", 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	free, err := st.CreateUser("free", "u2", "pw", "t2", 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	idle, err := st.CreateUser("idle", "u3", "pw", "t3", 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, id := range []int64{capped.ID, idle.ID} {
		if err := st.SetUserSpeedLimit(id, 20000); err != nil {
			t.Fatalf("set speed: %v", err)
		}
	}

	now := time.Now().Unix()
	for _, c := range []struct {
		id   int64
		ip   string
		seen int64
	}{
		{capped.ID, "1.1.1.1", now},
		{capped.ID, "2.2.2.2", now},
		{capped.ID, "3.3.3.3", now - 3600}, // stale: outside the window
		{free.ID, "4.4.4.4", now},
	} {
		if err := st.AddConnection(c.id, c.ip, c.seen); err != nil {
			t.Fatalf("add connection: %v", err)
		}
	}

	got, err := st.ShapedUsers(now - 600)
	if err != nil {
		t.Fatalf("shaped: %v", err)
	}
	if _, ok := got[free.ID]; ok {
		t.Error("a user with no cap was returned")
	}
	if len(got[capped.ID].IPs) != 2 {
		t.Errorf("capped user has %v, want the two recent addresses", got[capped.ID].IPs)
	}
	if got[capped.ID].Kbps != 20000 {
		t.Errorf("cap = %d, want 20000", got[capped.ID].Kbps)
	}
	// A capped user with no recent address is still reported, with none: the caller
	// drops them, and their silent absence would be indistinguishable from "not
	// capped".
	if t3, ok := got[idle.ID]; !ok || len(t3.IPs) != 0 {
		t.Errorf("idle capped user = %+v (present=%v), want present with no addresses", t3, ok)
	}
}

// One account must not be able to grow the kernel's classifier list without bound:
// every address it has been seen on inside the window would otherwise become a
// filter the kernel walks per packet.
func TestShapedUsersBoundsAddressesPerUser(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "speed3.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	u, err := st.CreateUser("roamer", "u1", "pw", "t1", 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.SetUserSpeedLimit(u.ID, 1000); err != nil {
		t.Fatalf("set speed: %v", err)
	}
	now := time.Now().Unix()
	for i := range model.MaxShapedIPsPerUser * 3 {
		ip := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		// Newest last, so the cap has to keep the RIGHT ones rather than the first
		// rows the join happens to return.
		if err := st.AddConnection(u.ID, ip, now-int64(model.MaxShapedIPsPerUser*3-i)); err != nil {
			t.Fatalf("add connection: %v", err)
		}
	}

	got, err := st.ShapedUsers(now - 600)
	if err != nil {
		t.Fatalf("shaped: %v", err)
	}
	ips := got[u.ID].IPs
	if len(ips) != model.MaxShapedIPsPerUser {
		t.Fatalf("%d addresses returned, want the cap of %d", len(ips), model.MaxShapedIPsPerUser)
	}
	// The newest sighting must be among them; the oldest must not.
	newest := fmt.Sprintf("10.0.%d.%d", (model.MaxShapedIPsPerUser*3-1)/256, (model.MaxShapedIPsPerUser*3-1)%256)
	if !slices.Contains(ips, newest) {
		t.Errorf("newest address %s missing from %v", newest, ips)
	}
	if slices.Contains(ips, "10.0.0.0") {
		t.Errorf("oldest address kept while newer ones exist: %v", ips)
	}
}

func TestShapedUsersSkipsDisabled(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "speed2.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	u, err := st.CreateUser("off", "u1", "pw", "t1", 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.SetUserSpeedLimit(u.ID, 1000); err != nil {
		t.Fatalf("set speed: %v", err)
	}
	if err := st.AddConnection(u.ID, "1.1.1.1", time.Now().Unix()); err != nil {
		t.Fatalf("add connection: %v", err)
	}
	if err := st.SetUserEnabled(u.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, err := st.ShapedUsers(time.Now().Unix() - 600)
	if err != nil {
		t.Fatalf("shaped: %v", err)
	}
	if len(got) != 0 {
		// A disabled account carries no traffic, so a class for it would sit in the
		// kernel matching nothing.
		t.Errorf("disabled user is still shaped: %+v", got)
	}
}
