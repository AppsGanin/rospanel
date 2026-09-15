package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A count for chosen users is the window count restricted to them: an address seen
// before the window does not count, and a user with nothing in it is simply absent.
func TestActiveDeviceCountsOfMatchesTheWindow(t *testing.T) {
	st := newStore(t)
	now := time.Now().Unix()
	var ids []int64
	for _, name := range []string{"a", "b", "c"} {
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	seen := map[int64][]int64{
		ids[0]: {now, now - 10, now - model.DeviceOnlineWindow - 60},
		ids[1]: {now - model.DeviceOnlineWindow - 60},
		ids[2]: {now},
	}
	for id, times := range seen {
		for i, at := range times {
			if err := st.AddConnection(id, "198.51.100."+string(rune('1'+i)), at); err != nil {
				t.Fatal(err)
			}
		}
	}
	since := now - model.DeviceOnlineWindow
	all, err := st.ActiveDeviceCounts(since)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.ActiveDeviceCountsOf(ids[:2], since)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int64]int{ids[0]: 2}; !reflect.DeepEqual(got, want) || all[ids[0]] != 2 || all[ids[2]] != 1 {
		t.Fatalf("counts of a and b %v, want %v (window counts %v)", got, want, all)
	}
	if got, err := st.ActiveDeviceCountsOf(nil, since); err != nil || len(got) != 0 {
		t.Fatalf("no users: %v %v", got, err)
	}
}
