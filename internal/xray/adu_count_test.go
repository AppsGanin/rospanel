package xray

import "testing"

// The count parsed from xray's output is what AddUsers trusts, so the parse itself
// has to be exact — including the "no such line" case, which must never read as a
// success.
func TestReportedAdded(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want int
	}{
		{"processing inbound: vless-in\nadd user: a\nresult: ok\nAdded 1 user(s) in total.", 1},
		{"processing inbound: hysteria-in\nunsupported inbound type\nAdded 0 user(s) in total.", 0},
		{"Added 12 user(s) in total.", 12},
		{"", -1},
		{"something else entirely", -1},
	} {
		if got := reportedAdded([]byte(tc.out)); got != tc.want {
			t.Errorf("reportedAdded(%q) = %d, want %d", tc.out, got, tc.want)
		}
	}
}

// The expected count has to match what xray reports back, so it must total the users
// across every shape AddUsers is handed. Undercounting turns a healthy add into a
// hard error that syncUsers then retries forever.
func TestCountInboundUsers(t *testing.T) {
	in := []Inbound{
		{Tag: "v", Settings: VLESSInboundSettings{Clients: []VLESSClient{{Email: "a"}, {Email: "b"}}}},
		{Tag: "t", Settings: TrojanInboundSettings{Clients: []TrojanClient{{Email: "c"}}}},
		{Tag: "h", Settings: HysteriaInboundSettings{Users: []HysteriaClient{{Email: "d"}, {Email: "e"}}}},
		{Tag: "unknown", Settings: struct{}{}},
	}
	if got, want := countInboundUsers(in), 5; got != want {
		t.Errorf("countInboundUsers = %d, want %d", got, want)
	}
	if got := countInboundUsers(nil); got != 0 {
		t.Errorf("countInboundUsers(nil) = %d, want 0", got)
	}
}
