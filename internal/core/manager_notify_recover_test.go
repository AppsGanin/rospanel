package core

import (
	"errors"
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
	// auto-rollback that then starts cleanly), and a stream of "снова работает" is
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
}

func TestFmtDowntime(t *testing.T) {
	cases := map[time.Duration]string{
		5 * time.Second:             "5 сек",
		90 * time.Second:            "1 мин",
		45 * time.Minute:            "45 мин",
		2*time.Hour + 5*time.Minute: "2 ч 5 мин",
	}
	for d, want := range cases {
		if got := fmtDowntime(d); got != want {
			t.Errorf("fmtDowntime(%s) = %q, want %q", d, got, want)
		}
	}
}
