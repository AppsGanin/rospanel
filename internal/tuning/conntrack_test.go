package tuning

import "testing"

func TestConntrackTarget(t *testing.T) {
	for _, c := range []struct{ ram, want int }{
		{0, ctMin},
		{961, ctMin}, // 1 GB box: the floor, eight times the kernel's 8192
		{2048, 131072},
		{64 << 10, ctMax},
	} {
		if got := ConntrackTarget(c.ram); got != c.want {
			t.Errorf("ConntrackTarget(%d) = %d, want %d", c.ram, got, c.want)
		}
	}
}
