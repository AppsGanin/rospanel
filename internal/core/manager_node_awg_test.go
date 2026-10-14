package core

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/awg"
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
	t.Parallel()
	m, st, n, msgs := awgNodeFixture(t)
	if _, ok := m.NodeAWG(n.ID); ok {
		t.Fatal("a node that reported nothing has a state")
	}
	t0 := time.Now()
	lookAt(t, m, st, n, t0)
	lookAt(t, m, st, n, t0.Add(laneHideAfter))
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
// can connect through. Said once when it has been down long enough to leave the
// subscriptions, and once when it is back.
func TestNodeAWGDownIsReportedOnceAndClears(t *testing.T) {
	t.Parallel()
	m, st, n, msgs := awgNodeFixture(t)

	report := func(running bool, errMsg string) {
		m.nodeGeoMu.Lock()
		m.nodeAWG[n.ID] = nodeAWGState{Running: running, Err: errMsg, Reported: true}
		m.nodeGeoMu.Unlock()
	}
	t0 := time.Now()
	report(true, "")
	lookAt(t, m, st, n, t0)
	if len(*msgs) != 0 {
		t.Fatalf("a healthy tunnel alerted: %v", *msgs)
	}

	// It goes down: nothing until it has been down for laneHideAfter, then one
	// message, not one per tick.
	report(false, "listen udp :51820: address already in use")
	lookAt(t, m, st, n, t0.Add(time.Second))
	if len(*msgs) != 0 {
		t.Fatalf("alerted before laneHideAfter: %v", *msgs)
	}
	for i := 0; i < 3; i++ {
		lookAt(t, m, st, n, t0.Add(laneHideAfter+time.Duration(i)*20*time.Second+time.Second))
	}
	if len(*msgs) != 1 {
		t.Fatalf("the outage was announced %d times, want once:\n%s", len(*msgs), strings.Join(*msgs, "\n---\n"))
	}
	if !strings.Contains((*msgs)[0], "address already in use") || !strings.Contains((*msgs)[0], "AmneziaWG") {
		t.Errorf("the message does not say what and why:\n%s", (*msgs)[0])
	}
	if lanes, _ := m.DownLanes(n.ID); !lanes[model.LaneAWG] {
		t.Error("the tunnel is down and still in the subscriptions")
	}
	// Health agrees.
	rep, _ := m.NodeHealth(n.ID)
	if c := findCheck(t, rep, "awg"); c.Status != healthError {
		t.Errorf("health check = %s, want an error", c.Status)
	}

	// And back up: one all-clear, then quiet.
	report(true, "")
	for i := 0; i < 3; i++ {
		lookAt(t, m, st, n, t0.Add(5*time.Minute+time.Duration(i)*20*time.Second))
	}
	if len(*msgs) != 2 {
		t.Fatalf("after recovery there were %d messages, want 2", len(*msgs))
	}
}

// A reported failure is a string from a remote machine on its way into an HTML
// message. An unescaped angle bracket makes Telegram reject the whole alert.
func TestNodeAWGErrorIsEscaped(t *testing.T) {
	t.Parallel()
	m, st, n, msgs := awgNodeFixture(t)
	m.nodeGeoMu.Lock()
	m.nodeAWG[n.ID] = nodeAWGState{Reported: true, Err: `bad <config> & worse`}
	m.nodeGeoMu.Unlock()
	t0 := time.Now()
	lookAt(t, m, st, n, t0)
	lookAt(t, m, st, n, t0.Add(laneHideAfter))

	if len(*msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(*msgs))
	}
	if strings.Contains((*msgs)[0], "<config>") {
		t.Errorf("the node's error went out unescaped:\n%s", (*msgs)[0])
	}
}

// lookAt runs the health loop's look at simulated time with the node reporting then,
// as a live node does every 20 seconds.
func lookAt(t *testing.T, m *Manager, st *store.Store, n *model.Node, at time.Time) {
	t.Helper()
	if err := st.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{LastSeen: at.Unix(), NodeVersion: "test", XrayRunning: true}); err != nil {
		t.Fatal(err)
	}
	m.observeLanesAt(at)
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
	t.Parallel()
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
	t.Parallel()
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

// A panel restart is not an outage: the master's own tunnel is still coming up when
// the health loop first looks, so a tunnel not running yet says nothing until it has
// been down for laneHideAfter — otherwise every restart sent "the tunnel is down" and
// then "the tunnel is back", which is what admins actually saw.
func TestLocalAWGRestartIsSilent(t *testing.T) {
	t.Parallel()
	m, st, _, msgs := awgNodeFixture(t)
	if err := st.SetProtocolEnabled("awg", true); err != nil {
		t.Fatalf("enable the master's AWG lane: %v", err)
	}
	if err := st.SetAWGConfig(51820, "", ""); err != nil {
		t.Fatalf("port: %v", err)
	}
	dev := &fakeAWG{}
	m.awg = dev

	t0 := time.Now()
	m.observeLanesAt(t0) // the moment the process starts
	dev.running = true   // up a few seconds later
	m.observeLanesAt(t0.Add(20 * time.Second))
	m.observeLanesAt(t0.Add(laneHideAfter + time.Minute))
	if len(*msgs) != 0 {
		t.Fatalf("a restart sent %d messages, want none:\n%v", len(*msgs), *msgs)
	}

	// Down for real: said once, not on every look after.
	dev.running, dev.err = false, "create tun awg0: operation not permitted"
	t1 := t0.Add(10 * time.Minute)
	m.observeLanesAt(t1)
	for i := 0; i < 4; i++ {
		m.observeLanesAt(t1.Add(laneHideAfter + time.Duration(i)*20*time.Second))
	}
	if len(*msgs) != 1 || !strings.Contains((*msgs)[0], "operation not permitted") {
		t.Fatalf("messages = %v, want one carrying the reason", *msgs)
	}
}

// fakeAWG is a tunnel whose state the test sets.
type fakeAWG struct {
	running bool
	err     string
}

func (f *fakeAWG) Apply(awg.Config) error                  { return nil }
func (f *fakeAWG) Stats() (map[string]awg.PeerStat, error) { return nil, nil }
func (f *fakeAWG) Running() bool                           { return f.running }
func (f *fakeAWG) LastError() string                       { return f.err }
func (f *fakeAWG) Close()                                  {}
func (f *fakeAWG) Retry() error                            { return nil }
