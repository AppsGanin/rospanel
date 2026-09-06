package core

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// awgNodeFixture is a joined, online node with the AmneziaWG lane switched on.
func awgNodeFixture(t *testing.T) (*Manager, *store.Store, *model.Node, *[]string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "awg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var msgs []string
	m := &Manager{store: st, nodeAWG: map[int64]nodeAWGState{}}
	m.SetAdminNotifier(func(html string) { msgs = append(msgs, html) })

	n, err := st.CreateNode("nl", "nl.example.com", "")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := st.SetNodeAWGEnabled(n.ID, true); err != nil {
		t.Fatalf("enable the AWG lane: %v", err)
	}
	if err := st.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen: time.Now().Unix(), NodeVersion: "test", XrayRunning: true,
	}); err != nil {
		t.Fatalf("status: %v", err)
	}
	fresh, err := st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	return m, st, fresh, &msgs
}

// An agent that has never mentioned the tunnel is not a tunnel that is down. Reading
// silence as a failure would alert on every node in the fleet the moment this ships,
// which is the fastest way to teach an operator to ignore the alert.
func TestNodeAWGSilenceIsNotAFailure(t *testing.T) {
	m, _, n, msgs := awgNodeFixture(t)
	if _, ok := m.NodeAWG(n.ID); ok {
		t.Fatal("a node that reported nothing has a state")
	}
	m.sweepAlerts([]model.Node{*n}, nil, time.Now())
	if len(*msgs) != 0 {
		t.Errorf("an agent that reports nothing raised %d alarms: %v", len(*msgs), *msgs)
	}
	// The health view says "unknown" rather than "down", and asks for an upgrade.
	rep, err := m.NodeHealth(n.ID)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	c := findCheck(t, rep, "awg")
	if c.Status != healthWarn || c.DetailKey != "health.nodeAWGUnknown" {
		t.Errorf("silent agent check = %s/%s, want a warning about not knowing", c.Status, c.DetailKey)
	}
}

// The gap this closes: the agent applies the tunnel, a failure goes into the node's
// own log, and the panel keeps the server green while issuing keys for a lane nobody
// can connect through.
func TestNodeAWGDownIsReportedOnceAndClears(t *testing.T) {
	m, _, n, msgs := awgNodeFixture(t)

	report := func(running bool, errMsg string) {
		m.nodeGeoMu.Lock()
		m.nodeAWG[n.ID] = nodeAWGState{Running: running, Err: errMsg, Reported: true}
		m.nodeGeoMu.Unlock()
	}

	// Baseline: the first sweep only records state.
	report(true, "")
	m.sweepAlerts([]model.Node{*n}, nil, time.Now())
	if len(*msgs) != 0 {
		t.Fatalf("a healthy tunnel alerted: %v", *msgs)
	}

	// It goes down, and stays down for several sweeps: one message, not one per tick.
	report(false, "listen udp :51820: address already in use")
	for i := 0; i < 3; i++ {
		m.sweepAlerts([]model.Node{*n}, nil, time.Now())
	}
	if len(*msgs) != 1 {
		t.Fatalf("the outage was announced %d times, want once:\n%s", len(*msgs), strings.Join(*msgs, "\n---\n"))
	}
	if !strings.Contains((*msgs)[0], "address already in use") {
		t.Errorf("the message does not carry the reason:\n%s", (*msgs)[0])
	}
	// Health agrees.
	rep, _ := m.NodeHealth(n.ID)
	if c := findCheck(t, rep, "awg"); c.Status != healthError {
		t.Errorf("health check = %s, want an error", c.Status)
	}

	// And back up: one all-clear, then quiet.
	report(true, "")
	for i := 0; i < 3; i++ {
		m.sweepAlerts([]model.Node{*n}, nil, time.Now())
	}
	if len(*msgs) != 2 {
		t.Fatalf("after recovery there were %d messages, want 2", len(*msgs))
	}
}

// A reported failure is a string from a remote machine on its way into an HTML
// message. An unescaped angle bracket makes Telegram reject the whole alert.
func TestNodeAWGErrorIsEscaped(t *testing.T) {
	m, _, n, msgs := awgNodeFixture(t)
	m.nodeGeoMu.Lock()
	m.nodeAWG[n.ID] = nodeAWGState{Running: true, Reported: true}
	m.nodeGeoMu.Unlock()
	m.sweepAlerts([]model.Node{*n}, nil, time.Now()) // baseline

	m.nodeGeoMu.Lock()
	m.nodeAWG[n.ID] = nodeAWGState{Reported: true, Err: `bad <config> & worse`}
	m.nodeGeoMu.Unlock()
	m.sweepAlerts([]model.Node{*n}, nil, time.Now())

	if len(*msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(*msgs))
	}
	if strings.Contains((*msgs)[0], "<config>") {
		t.Errorf("the node's error went out unescaped:\n%s", (*msgs)[0])
	}
}

// findCheck pulls one check out of a health report.
func findCheck(t *testing.T, rep *HealthReport, key string) HealthCheck {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("health report has no %q check", key)
	return HealthCheck{}
}
