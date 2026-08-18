package model

import "testing"

// DeviceCap is the number both the roster enforcement and every display read, so the two
// can never disagree — which they did while it returned 0 for "no limit" and the store
// still refused the 51st device, telling the user "51 / 0". It must never return 0, and
// it must never quietly shrink a number the operator chose.
func TestDeviceCap(t *testing.T) {
	for _, c := range []struct {
		name     string
		fallback int
		user     int
		want     int
	}{
		{name: "nothing set anywhere", want: MaxDevicesPerUser},
		{name: "panel-wide fallback", fallback: 3, want: 3},
		{name: "the user's own limit wins", fallback: 3, user: 5, want: 5},
		{name: "an explicit limit above the default is honoured", user: 100, want: 100},
		{name: "a fallback above the default is honoured", fallback: 80, want: 80},
	} {
		t.Run(c.name, func(t *testing.T) {
			set := &Settings{HWIDFallbackLimit: c.fallback}
			if got := set.DeviceCap(User{DeviceLimit: c.user}); got != c.want {
				t.Errorf("DeviceCap = %d, want %d", got, c.want)
			}
		})
	}
}
