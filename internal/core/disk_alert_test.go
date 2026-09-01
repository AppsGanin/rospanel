package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/sysstat"
)

const gb = int64(1) << 30

// Nothing else in the panel watches free space: when the disk fills, SQLite stops
// writing, and that surfaces as traffic going unrecorded and users not syncing —
// symptoms an operator has no reason to connect to a full disk, on a node they have no
// reason to be logged into.
func TestDiskAlertFiresAndClears(t *testing.T) {
	lang := i18n.Lang("ru")

	// Comfortable: nothing to say, and no alarm to remember.
	if on, msg := diskAlert(false, 50*gb, 100*gb, "srv", lang); on || msg != "" {
		t.Errorf("a half-empty disk raised an alarm: %v %q", on, msg)
	}
	// Below the threshold the health report already calls a warning.
	on, msg := diskAlert(false, 90*gb, 100*gb, "srv", lang)
	if !on || msg == "" {
		t.Fatalf("10%% free raised nothing: %v %q", on, msg)
	}
	if !strings.Contains(msg, "srv") {
		t.Errorf("the alert does not say which server: %q", msg)
	}
	// Still low: the operator has already been told, and being told again every
	// sweep is how an alert becomes noise.
	if _, msg := diskAlert(true, 90*gb, 100*gb, "srv", lang); msg != "" {
		t.Errorf("repeated the same alarm: %q", msg)
	}
	// Freed up past the clear threshold.
	if on, msg := diskAlert(true, 70*gb, 100*gb, "srv", lang); on || msg == "" {
		t.Errorf("no all-clear after space was freed: %v %q", on, msg)
	}
}

// A disk sitting exactly on the line must not alternate between alarm and all-clear on
// every sweep — which is what a single threshold would do, and what teaches an operator
// to ignore the alert.
func TestDiskAlertDoesNotFlapOnTheBoundary(t *testing.T) {
	lang := i18n.Lang("ru")
	// 16% free: above the 15% alarm, below the 20% all-clear.
	if on, msg := diskAlert(true, 84*gb, 100*gb, "srv", lang); !on || msg != "" {
		t.Errorf("cleared the alarm while still low: %v %q", on, msg)
	}
	if on, msg := diskAlert(false, 84*gb, 100*gb, "srv", lang); on || msg != "" {
		t.Errorf("raised an alarm above the threshold: %v %q", on, msg)
	}
}

// An older node, or one whose first report has not arrived, sends no figures at all.
// Zero must read as "no data", never as "the disk is full".
func TestDiskAlertSaysNothingWithoutFigures(t *testing.T) {
	if on, msg := diskAlert(false, 0, 0, "srv", i18n.Lang("ru")); on || msg != "" {
		t.Errorf("a node with no disk figures was reported as full: %v %q", on, msg)
	}
}

// A supervised recovery has to re-push the user set, not just announce itself.
//
// The supervisor restores config.json.bak to get Xray running after a crash, and that
// backup is only refreshed by Apply — a user sync moves config.json without touching
// it. So the config that ends an outage can be well out of date on users, and nothing
// else would catch it: reconcileLoop is driven by events, not a timer, and the
// rollback fires no event of its own.
func TestRecoveryReSyncsUsers(t *testing.T) {
	m := &Manager{reconcileCh: make(chan struct{}, 1)}
	m.onXrayRecover()
	select {
	case <-m.reconcileCh:
	default:
		t.Error("Xray came back and nobody re-sent the user set — anyone added since " +
			"the restored backup stays unserved until an unrelated edit triggers a sync")
	}
}

// The rollback message has to name the reason Xray refused the config: that string is
// the only thing connecting a brief outage to the setting the operator has to go and
// fix. It is also the only part of the message that comes from outside, so it is
// escaped like everything else that reaches an HTML-parsed chat.
func TestRollbackMessageCarriesTheReason(t *testing.T) {
	lang := i18n.Lang("ru")
	msg := fmt.Sprintf(i18n.T(lang, "notify.configRolledBack"), model.LocalNodeName,
		escHTML("common/geodata: CIDR prefix length 96 exceeds max 32"))
	if !strings.Contains(msg, "CIDR prefix length 96") {
		t.Errorf("the reason did not survive into the message: %q", msg)
	}
	esc := fmt.Sprintf(i18n.T(lang, "notify.configRolledBack"), model.LocalNodeName,
		escHTML(`bad <b>rule</b> & "quote"`))
	if strings.Contains(esc, "<b>rule</b>") {
		t.Errorf("the reason reached an HTML message unescaped: %q", esc)
	}
}

// The master is not in ListNodes — it is a virtual node the API view assembles — so it
// needs its own path through the sweep, and it is the machine whose full disk breaks
// everything at once. This walks that path rather than the rule underneath it: having
// the rule right and never calling it is the failure mode that only shows up in
// production.
func TestMasterDiskAlertGoesThroughTheSweep(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "disk.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st, nodeAlerts: map[int64]*nodeAlertState{}}
	live := map[int64]struct{}{}

	if msg := m.localDiskAlertMsg(live, 50*gb, 100*gb); msg != "" {
		t.Errorf("a half-empty disk on the master raised: %q", msg)
	}
	msg := m.localDiskAlertMsg(live, 92*gb, 100*gb)
	if msg == "" {
		t.Fatal("the master's disk filled up and nobody was told")
	}
	if !strings.Contains(msg, model.LocalNodeName) {
		t.Errorf("the alert does not name the master: %q", msg)
	}
	// The state has to survive the prune that follows it in the sweep, or the alarm
	// re-fires from scratch every pass.
	if _, ok := live[model.LocalNodeID]; !ok {
		t.Error("the master's alert state would be pruned as stale after every sweep")
	}
	if again := m.localDiskAlertMsg(live, 92*gb, 100*gb); again != "" {
		t.Errorf("repeated the same alarm on the next sweep: %q", again)
	}
}

// Walks the sweep itself, not the rule underneath it. Deleting the master's disk check
// from SweepNodeAlerts used to break nothing: every test drove the rule directly, so a
// correct rule that nobody called would have shipped.
func TestSweepConsultsTheMastersDisk(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sweep.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	m := &Manager{store: st, nodeAlerts: map[int64]*nodeAlertState{}}

	m.sweepAlerts(nil, &sysstat.Stats{DiskUsed: 92 * gb, DiskTotal: 100 * gb}, time.Now())

	m.nodeAlertMu.Lock()
	alerted := m.nodeAlerts[model.LocalNodeID] != nil && m.nodeAlerts[model.LocalNodeID].diskLowAlerted
	m.nodeAlertMu.Unlock()
	if !alerted {
		t.Error("the sweep never looked at the master's disk — the machine whose full " +
			"disk breaks everything at once is the one it skips")
	}
}
