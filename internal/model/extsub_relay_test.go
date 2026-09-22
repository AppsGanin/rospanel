package model

import "testing"

// Routes skip what a random UUID carries there (0x4000–0x4FFF), so no user's own UUID
// names a relayed server, and stop where two bytes do.
func TestExtRoute(t *testing.T) {
	for _, c := range []struct {
		id    int64
		route uint16
		ok    bool
	}{
		{0, 0, false}, {1, 1, true}, {0x3FFF, 0x3FFF, true},
		{0x4000, 0x5000, true}, {0xEFFF, 0xFFFF, true}, {0xF000, 0, false},
	} {
		if r, ok := ExtRoute(c.id); r != c.route || ok != c.ok {
			t.Errorf("ExtRoute(%#x) = %#x %v, want %#x %v", c.id, r, ok, c.route, c.ok)
		}
	}
	for id := int64(1); id <= 0xEFFF; id++ {
		if r, _ := ExtRoute(id); r >= 0x4000 && r <= 0x4FFF {
			t.Fatalf("ExtRoute(%#x) = %#x, inside the version-4 range", id, r)
		}
	}
}

// The route goes into the 7th and 8th bytes and nowhere else, and reads back.
func TestRouteUUID(t *testing.T) {
	got, ok := RouteUUID("6BBD16CD-DFC1-47C4-9426-59B57B92B173", 0x0005)
	if !ok || got != "6bbd16cd-dfc1-0005-9426-59b57b92b173" {
		t.Fatalf("RouteUUID = %q %v", got, ok)
	}
	if r, ok := UUIDRoute(got); !ok || r != 5 {
		t.Fatalf("UUIDRoute = %d %v", r, ok)
	}
	if r, ok := UUIDRoute("6bbd16cd-dfc1-47c4-9426-59b57b92b173"); !ok || r != 0x47c4 {
		t.Fatalf("own route = %#x %v", r, ok)
	}
	for _, bad := range []string{"", "uuid", "6bbd16cd-dfc1-47c4-9426-59b57b92b17", "6bbd16cdXdfc1-47c4-9426-59b57b92b173", "6bbd16cd-dfc1-47c4-9426-59b57b92b17g"} {
		if _, ok := RouteUUID(bad, 1); ok {
			t.Errorf("RouteUUID(%q) accepted", bad)
		}
	}
}
