package core

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
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
