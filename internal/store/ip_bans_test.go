package store

import (
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A ban is recorded once, keeps its first time when placed again, and comes out.
func TestIPBans(t *testing.T) {
	s := openTestStore(t)
	for _, b := range []model.IPBan{
		{IP: "203.0.113.7", UserID: 3, At: 100},
		{IP: "2001:db8::5", UserID: 0, At: 200},
		{IP: "203.0.113.7", UserID: 4, At: 300}, // again, from another user
	} {
		if err := s.BanIP(b); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListIPBans()
	if err != nil {
		t.Fatal(err)
	}
	want := []model.IPBan{{IP: "2001:db8::5", At: 200}, {IP: "203.0.113.7", UserID: 4, At: 100}}
	if !slices.Equal(list, want) {
		t.Errorf("bans = %+v, want %+v", list, want)
	}
	if ips, _ := s.BannedIPList(); !slices.Equal(ips, []string{"2001:db8::5", "203.0.113.7"}) {
		t.Errorf("banned list = %v", ips)
	}
	if gone, err := s.UnbanIP("203.0.113.7"); err != nil || !gone {
		t.Fatalf("unban: %v %v", gone, err)
	}
	if gone, _ := s.UnbanIP("203.0.113.7"); gone {
		t.Error("an address unbanned twice")
	}
	if ips, _ := s.BannedIPList(); !slices.Equal(ips, []string{"2001:db8::5"}) {
		t.Errorf("after unban = %v", ips)
	}
}
