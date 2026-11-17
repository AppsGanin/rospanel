package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Device keys and users' own keys share one slot space; the cap holds under
// concurrent requests; a user's deletion frees their devices' slots.
func TestAWGDevicesShareSlotsAndHoldTheCap(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	a, err := st.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := st.CreateUser("b", "uuid-b", "pw", "tok-b", 0, 0, 0)
	if _, err := st.ClaimUsersAWG([]AWGClaim{{a.ID, "key-a"}}, 2, 10); err != nil {
		t.Fatal(err)
	}
	d, err := st.CreateAWGDevice(model.AWGDevice{UserID: a.ID, PrivateKey: "dev-1"}, 5, 2, 10)
	if err != nil || d.Slot != 3 {
		t.Fatalf("device slot %+v err=%v, want 3 (2 is a's own)", d, err)
	}
	got, _ := st.ClaimUsersAWG([]AWGClaim{{b.ID, "key-b"}}, 2, 10)
	if got[b.ID].Slot != 4 {
		t.Fatalf("b got slot %d, want 4: a device's slot is taken too", got[b.ID].Slot)
	}

	// Cap 5 with the main key: 4 devices at most, however many ask at once.
	var wg sync.WaitGroup
	var mu sync.Mutex
	full := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.CreateAWGDevice(model.AWGDevice{UserID: a.ID, PrivateKey: "k"}, 5, 2, 100)
			if errors.Is(err, ErrAWGKeysFull) {
				mu.Lock()
				full++
				mu.Unlock()
			} else if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	list, _ := st.ListAWGDevices(a.ID)
	if len(list) != 4 || full != 5 {
		t.Fatalf("%d devices, %d refused; want 4 and 5", len(list), full)
	}
	if list[0].PrivateKey != "dev-1" {
		t.Errorf("the key did not round-trip: %q", list[0].PrivateKey)
	}

	// A hwid gets its key back rather than a second one.
	h1, _ := st.CreateAWGDevice(model.AWGDevice{UserID: b.ID, HWID: "hw", PrivateKey: "p1"}, 5, 2, 100)
	h2, _ := st.CreateAWGDevice(model.AWGDevice{UserID: b.ID, HWID: "hw", PrivateKey: "p2"}, 5, 2, 100)
	if h1.ID != h2.ID || h2.PrivateKey != "p1" {
		t.Fatalf("hwid made a second key: %+v %+v", h1, h2)
	}

	// The working set carries them.
	users, _ := st.WorkingCredentials(1)
	for _, u := range users {
		if u.ID == a.ID && len(u.AWGDevices) != 4 {
			t.Errorf("a carries %d device keys, want 4", len(u.AWGDevices))
		}
	}

	// Deleting the user frees their devices.
	if err := st.DeleteUser(a.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListAWGDevices(a.ID); len(list) != 0 {
		t.Errorf("a deleted user's devices stayed: %d", len(list))
	}
}
