package core

import (
	"errors"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// An alarm with no all-clear leaves the operator unable to tell "recovered in two
// seconds" from "still down". The pairing has to hold in both directions: an
// all-clear for an alarm that was throttled away would announce the end of an outage
// nobody was told about.
func TestXrayCrashAndRecoveryArePaired(t *testing.T) {
	m := bulkTestManager(t)
	var sent []string
	m.SetAdminNotifier(func(html string) { sent = append(sent, html) })
	if err := m.store.SetAdminEvents(model.AdminEventXrayDown); err != nil {
		t.Fatalf("enable category: %v", err)
	}

	// Recovery with no preceding alarm says nothing — a routine restart is not news.
	m.onXrayRecover()
	if len(sent) != 0 {
		t.Fatalf("all-clear sent without an alarm: %v", sent)
	}

	m.onXrayCrash(errors.New("boom"))
	if len(sent) != 1 {
		t.Fatalf("crash alerts = %d, want 1", len(sent))
	}
	m.onXrayRecover()
	if len(sent) != 2 {
		t.Fatalf("no all-clear after an alarm: %v", sent)
	}

	// And it fires once: the supervisor may report recovery more than once (an
	// auto-rollback that then starts cleanly), and a stream of "working again" is
	// its own kind of noise.
	m.onXrayRecover()
	if len(sent) != 2 {
		t.Fatalf("all-clear repeated: %v", sent)
	}

	// A crash inside the throttle window raises no alarm, so its recovery must stay
	// quiet too — otherwise a crash loop reports only good news.
	m.onXrayCrash(errors.New("again"))
	m.onXrayRecover()
	if len(sent) != 2 {
		t.Fatalf("throttled crash produced an all-clear: %v", sent)
	}

	// onXrayWedged with restarted=false (auto-restart off) is an active outage,
	// so recovery must send the all-clear.
	m.lastCrashNotify = time.Now().Add(-crashNotifyThrottle - time.Minute)
	m.onXrayWedged(false)
	if len(sent) != 3 {
		t.Fatalf("wedged (no restart) alerts = %d, want 3", len(sent))
	}
	m.onXrayRecover()
	if len(sent) != 4 {
		t.Fatalf("no all-clear after wedged (no restart): %v", sent)
	}

	// onXrayWedged with restarted=true was already self-resolved, so recovery
	// should not produce an extra all-clear.
	m.lastCrashNotify = time.Now().Add(-crashNotifyThrottle - time.Minute)
	m.onXrayWedged(true)
	if len(sent) != 5 {
		t.Fatalf("wedged (restarted) alerts = %d, want 5", len(sent))
	}
	m.onXrayRecover()
	if len(sent) != 5 {
		t.Fatalf("unexpected all-clear after wedged (restarted): %v", sent)
	}
}

func TestFmtDowntime(t *testing.T) {
	cases := map[time.Duration]string{
		5 * time.Second:             "5 сек",
		90 * time.Second:            "1 мин",
		45 * time.Minute:            "45 мин",
		2*time.Hour + 5*time.Minute: "2 ч 5 мин",
	}
	for d, want := range cases {
		if got := fmtDowntime(d, i18n.RU); got != want {
			t.Errorf("fmtDowntime(%s, i18n.RU) = %q, want %q", d, got, want)
		}
	}
}
