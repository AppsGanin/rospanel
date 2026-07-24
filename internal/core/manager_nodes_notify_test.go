package core

import (
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// nodeAlertFixture builds a manager with a capturing notifier and one joined,
// enabled node, already baselined as online with Xray up — the state every test
// below transitions away from.
func nodeAlertFixture(t *testing.T) (*Manager, *[]string, *model.Node, time.Time) {
	t.Helper()
	m, msgs := newNotifyManager(t)
	n, err := m.store.CreateNode("NL", "203.0.113.7", "")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	now := time.Now()
	n.LastSeen = now.Unix()
	n.ConfigHash = "h1" // Joined()
	n.XrayRunning = true
	if got := m.nodeAlertsFor(n, now); got != nil {
		t.Fatalf("baseline emitted alerts: %v", got)
	}
	return m, msgs, n, now
}

// only returns the single message the sweep produced, failing otherwise — every
// transition below is expected to say exactly one thing.
func only(t *testing.T, out []nodeAlertMsg) nodeAlertMsg {
	t.Helper()
	if len(out) != 1 {
		t.Fatalf("expected exactly one alert, got %d: %v", len(out), out)
	}
	return out[0]
}

// TestNodeOfflineTransition: going silent alerts once, staying silent stays quiet,
// and coming back sends the all-clear.
func TestNodeOfflineTransition(t *testing.T) {
	m, _, n, now := nodeAlertFixture(t)

	// last_seen is now older than the online window ⇒ unreachable.
	later := now.Add(10 * time.Minute)
	got := only(t, m.nodeAlertsFor(n, later))
	if got.bit != model.AdminEventXrayDown || !strings.Contains(got.html, "Нет связи с сервером") {
		t.Fatalf("expected an unreachable alert, got %+v", got)
	}
	if !strings.Contains(got.html, "NL") || !strings.Contains(got.html, "203.0.113.7") {
		t.Fatalf("alert does not name the node: %s", got.html)
	}

	if out := m.nodeAlertsFor(n, later.Add(time.Minute)); out != nil {
		t.Fatalf("still-offline node re-alerted: %v", out)
	}

	n.LastSeen = later.Add(2 * time.Minute).Unix() // it synced again
	back := only(t, m.nodeAlertsFor(n, later.Add(2*time.Minute)))
	if !strings.Contains(back.html, "восстановлена") {
		t.Fatalf("expected a recovery alert, got %+v", back)
	}
}

// TestNodeOfflineRecoveryOnlyAfterAlert: a node that was never reported down must
// not produce an all-clear, mirroring the master's crashAlerted rule.
func TestNodeOfflineRecoveryOnlyAfterAlert(t *testing.T) {
	m, _, n, now := nodeAlertFixture(t)
	// Never observed offline (no sweep landed in the gap): last_seen jumps forward.
	n.LastSeen = now.Add(10 * time.Minute).Unix()
	if out := m.nodeAlertsFor(n, now.Add(10*time.Minute)); out != nil {
		t.Fatalf("unprompted all-clear: %v", out)
	}
}

// TestNodeXrayTransition: a node reporting its Xray down alerts once and reports
// the recovery, and the alert is throttled while it flaps.
func TestNodeXrayTransition(t *testing.T) {
	m, _, n, now := nodeAlertFixture(t)

	n.XrayRunning = false
	n.LastSeen = now.Unix()
	down := only(t, m.nodeAlertsFor(n, now))
	if down.bit != model.AdminEventXrayDown || !strings.Contains(down.html, "аварийно завершился") {
		t.Fatalf("expected an Xray crash alert, got %+v", down)
	}

	// Up, then straight back down inside the throttle window: the recovery is sent
	// (admins saw the alarm), the second crash is suppressed.
	n.XrayRunning = true
	n.LastSeen = now.Add(time.Minute).Unix()
	up := only(t, m.nodeAlertsFor(n, now.Add(time.Minute)))
	if !strings.Contains(up.html, "снова работает") {
		t.Fatalf("expected a recovery alert, got %+v", up)
	}
	n.XrayRunning = false
	n.LastSeen = now.Add(2 * time.Minute).Unix()
	if out := m.nodeAlertsFor(n, now.Add(2*time.Minute)); out != nil {
		t.Fatalf("crash alert not throttled: %v", out)
	}
}

// TestNodeXrayIgnoredWhileOffline: a silent node's last report is stale, so it must
// not raise a second alarm on top of the unreachable one.
func TestNodeXrayIgnoredWhileOffline(t *testing.T) {
	m, _, n, now := nodeAlertFixture(t)
	later := now.Add(10 * time.Minute)
	n.XrayRunning = false // whatever the frozen row says
	got := only(t, m.nodeAlertsFor(n, later))
	if !strings.Contains(got.html, "Нет связи с сервером") {
		t.Fatalf("expected only the unreachable alert, got %+v", got)
	}
}

// TestNodeCertAlerts: a CA-signed fingerprint change is an event, the self-signed
// fallback is not, and a reported ACME failure alerts once per throttle window.
func TestNodeCertAlerts(t *testing.T) {
	m, _, n, now := nodeAlertFixture(t)

	// Self-signed fallback changing: silent.
	n.CertSHA256, n.CertSelfSigned = "aa", true
	if out := m.nodeAlertsFor(n, now); out != nil {
		t.Fatalf("self-signed cert alerted: %v", out)
	}

	// First real cert.
	n.CertSHA256, n.CertSelfSigned = "bb", false
	n.CertExpiresAt = now.Add(90 * 24 * time.Hour).Unix()
	first := only(t, m.nodeAlertsFor(n, now))
	if first.bit != model.AdminEventCert || !strings.Contains(first.html, "выпущен") {
		t.Fatalf("expected an issued-cert alert, got %+v", first)
	}
	if !strings.Contains(first.html, "89 дн.") && !strings.Contains(first.html, "90 дн.") {
		t.Fatalf("alert lost the expiry: %s", first.html)
	}

	// A later renewal.
	n.CertSHA256 = "cc"
	renew := only(t, m.nodeAlertsFor(n, now))
	if !strings.Contains(renew.html, "обновлён") {
		t.Fatalf("expected a renewal alert, got %+v", renew)
	}

	// A failure the node reported: once, then throttled.
	m.NoteNodeCertError(n.ID, "acme: dns problem")
	fail := only(t, m.nodeAlertsFor(n, now))
	if fail.bit != model.AdminEventCert || !strings.Contains(fail.html, "dns problem") {
		t.Fatalf("expected a cert-failure alert, got %+v", fail)
	}
	within := now.Add(time.Hour)
	n.LastSeen = within.Unix() // still syncing, so only the throttle can silence it
	if out := m.nodeAlertsFor(n, within); out != nil {
		t.Fatalf("cert failure not throttled: %v", out)
	}
	after := now.Add(certErrNotifyThrottle + time.Minute)
	n.LastSeen = after.Unix()
	if out := m.nodeAlertsFor(n, after); len(out) != 1 {
		t.Fatalf("cert failure did not repeat after the window: %v", out)
	}
}

// TestNodeAlertsRespectAdminMask: both node categories go through the same settings
// gate as the master's, so switching the category off silences them.
func TestNodeAlertsRespectAdminMask(t *testing.T) {
	m, msgs, n, now := nodeAlertFixture(t)
	if err := m.store.SetAdminEvents(0); err != nil {
		t.Fatalf("clear admin events: %v", err)
	}
	for _, a := range m.nodeAlertsFor(n, now.Add(10*time.Minute)) {
		m.notifyAdminEvent(a.bit, a.html)
	}
	if len(*msgs) != 0 {
		t.Fatalf("alert sent with the category off: %v", *msgs)
	}
}

// TestSweepNodeAlertsSkipsDisabled: a node switched off in the panel is not an
// outage, and re-enabling it must start from a fresh baseline rather than announce
// the operator's own decision.
func TestSweepNodeAlertsSkipsDisabled(t *testing.T) {
	m, msgs := newNotifyManager(t)
	n, err := m.store.CreateNode("NL", "203.0.113.7", "")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := m.store.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{
		LastSeen: time.Now().Unix(), XrayRunning: true, ConfigHash: "h1",
	}); err != nil {
		t.Fatalf("status: %v", err)
	}
	m.SweepNodeAlerts() // baseline, online

	if err := m.store.SetNodeEnabled(n.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	m.SweepNodeAlerts() // long enough offline to trip, but disabled ⇒ silent
	m.SweepNodeAlerts()
	if len(*msgs) != 0 {
		t.Fatalf("disabled node alerted: %v", *msgs)
	}
	m.nodeAlertMu.Lock()
	_, tracked := m.nodeAlerts[n.ID]
	m.nodeAlertMu.Unlock()
	if tracked {
		t.Fatal("disabled node still tracked — re-enabling would replay its state")
	}
}
