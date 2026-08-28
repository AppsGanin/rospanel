package geo

import "testing"

func TestFlag(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"NL", "🇳🇱"},
		{"us", "🇺🇸"}, // lower case is what some tables emit
		{"", ""},
		{"X", ""},
		{"USA", ""},
		{"1A", ""}, // digits are not regional indicators
	} {
		if got := Flag(tc.in); got != tc.want {
			t.Errorf("Flag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
