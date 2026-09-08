package core

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
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
	if c.Status != healthWarn || c.DetailKey != "health.awgUnknown" {
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

// The master runs its own tunnel in this process, and its diagnostics never mentioned
// it — the one server whose logs the operator can actually read was the one the panel
// said nothing about. The lane being switched on is what makes it a question at all.
func TestMasterAWGAppearsInDiagnosticsOnlyWhenTheLaneIsOn(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "mawg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	// Health() walks every check, including the Xray one, so the manager needs a
	// supervisor even though this test is about the tunnel.
	m := &Manager{
		store:   st,
		sup:     xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir),
		nodeAWG: map[int64]nodeAWGState{},
	}

	// Lane off: no row at all, rather than a green one for a tunnel nobody asked for.
	if hasCheck(m.Health(), "awg") {
		t.Error("the tunnel is reported while the lane is switched off")
	}

	if err := st.SetProtocolEnabled("awg", true); err != nil {
		t.Fatalf("switch the lane on: %v", err)
	}
	rep := m.Health()
	if !hasCheck(rep, "awg") {
		t.Fatalf("the lane is on and the tunnel is still missing from diagnostics; checks: %v", checkKeys(rep))
	}
	// No tunnel is actually running in a test process, so the check must say so rather
	// than report ok — a green row for a tunnel that does not exist is the worse bug.
	c := findCheck(t, rep, "awg")
	if c.Status != healthError {
		t.Errorf("check = %s, want an error when the lane is on and the tunnel is not up", c.Status)
	}
}

func hasCheck(rep *HealthReport, key string) bool {
	for _, c := range rep.Checks {
		if c.Key == key {
			return true
		}
	}
	return false
}

func checkKeys(rep *HealthReport) []string {
	out := make([]string, 0, len(rep.Checks))
	for _, c := range rep.Checks {
		out = append(out, c.Key)
	}
	return out
}

// Where the row sits is part of the answer: the tunnel is a lane this server serves,
// so it reads next to the Xray config rather than at the end of the list, and the
// master and node views must not order the same facts differently.
func TestAWGSitsUnderTheXrayConfigRow(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "order.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{
		store:   st,
		sup:     xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir),
		nodeAWG: map[int64]nodeAWGState{},
	}
	if err := st.SetProtocolEnabled("awg", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	assertAfterConfig(t, "master", checkKeys(m.Health()))

	// The node view, built from a different function, has to agree.
	n, err := st.CreateNode("nl", "nl.example.com", "")
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	if err := st.SetNodeAWGEnabled(n.ID, true); err != nil {
		t.Fatalf("node lane: %v", err)
	}
	if err := st.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen: time.Now().Unix(), NodeVersion: "test", XrayRunning: true,
	}); err != nil {
		t.Fatalf("status: %v", err)
	}
	rep, err := m.NodeHealth(n.ID)
	if err != nil {
		t.Fatalf("node health: %v", err)
	}
	assertAfterConfig(t, "node", checkKeys(rep))
}

func assertAfterConfig(t *testing.T, view string, keys []string) {
	t.Helper()
	cfg, awg := -1, -1
	for i, k := range keys {
		switch k {
		case "config":
			cfg = i
		case "awg":
			awg = i
		}
	}
	if cfg < 0 || awg < 0 {
		t.Fatalf("%s view is missing a row; got %v", view, keys)
	}
	if awg != cfg+1 {
		t.Errorf("%s view: awg is at %d and config at %d, want it directly after; got %v",
			view, awg, cfg, keys)
	}
}

// A panel restart is not an outage. The alert sweep runs the moment the process
// starts, while the master's own tunnel is still coming up, so the first pass has to
// record a baseline and say nothing — otherwise every restart sent "the tunnel is
// down" and then "the tunnel is back", which is what admins actually saw.
func TestLocalAWGFirstSweepIsSilent(t *testing.T) {
	m, st, _, msgs := awgNodeFixture(t)
	if err := st.SetProtocolEnabled("awg", true); err != nil {
		t.Fatalf("enable the master's AWG lane: %v", err)
	}

	// m.awg is nil here, so the tunnel reads as not running — the state a restart
	// catches it in.
	m.sweepAlerts(nil, nil, time.Now())
	if len(*msgs) != 0 {
		t.Fatalf("the first sweep sent %d messages, want none:\n%v", len(*msgs), *msgs)
	}

	// Still down a minute later is a real outage, and that one is worth saying.
	m.sweepAlerts(nil, nil, time.Now())
	if len(*msgs) != 1 {
		t.Fatalf("the second sweep sent %d messages, want 1:\n%v", len(*msgs), *msgs)
	}

	// And it is said once, not on every sweep after.
	m.sweepAlerts(nil, nil, time.Now())
	if len(*msgs) != 1 {
		t.Fatalf("the outage was repeated: %d messages, want 1", len(*msgs))
	}
}
