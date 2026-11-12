package logbuf

import "testing"

func TestRoute(t *testing.T) {
	split.Store(false)
	if got := Route("opera: started", 0, false); got != ScopePanel {
		t.Fatalf("split off: got %v, want panel", got)
	}
	SplitServer()
	defer split.Store(false)
	cases := []struct {
		msg  string
		id   int64
		has  bool
		want Scope
	}{
		{"opera: helper ready", 0, false, ScopeServer},
		{"rospanel: opera: PROXY   : handler.go:133: ...", 0, false, ScopeServer},
		{"rospanel: [WARN] connguard: per-IP limits installed", 0, false, ScopeServer},
		{"xray: started", 0, false, ScopeServer},
		{"reconcile: config applied", 0, false, ScopeServer},
		{"awg: tunnel up", 0, false, ScopeServer},
		{"node: pushing a change of users", 7, true, ScopeNode},
		{"warp: node registration failed", 7, true, ScopeNode},
		{"subscriptions: lane down", 3, true, ScopeNode},
		{"subscriptions: lane down", 0, true, ScopeServer},
		{"uptime: sample failed", 0, true, ScopeServer},
		{"node traffic: cannot list nodes", 0, false, ScopePanel},
		{"billing: user downgraded", 0, false, ScopePanel},
		{"rospanel: startup: ready", 0, false, ScopePanel},
		{"user 175 enabled=false", 0, false, ScopePanel},
	}
	for _, c := range cases {
		if got := Route(c.msg, c.id, c.has); got != c.want {
			t.Errorf("Route(%q, %d, %v) = %v, want %v", c.msg, c.id, c.has, got, c.want)
		}
	}
}

func TestNodeHub(t *testing.T) {
	_, _ = Node(5).Write([]byte("a\n"))
	if got := Node(5).Tail(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("Node(5) tail = %v", got)
	}
	ForgetNode(5)
	if got := Node(5).Tail(); len(got) != 0 {
		t.Fatalf("after ForgetNode tail = %v", got)
	}
}
