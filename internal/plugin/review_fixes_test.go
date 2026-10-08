package plugin

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A caller that goes away does not kill the VM or count against the plugin: ten
// disconnected requests used to pause it.
func TestCancelledCallersDoNotPause(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < breakerLimit+2; i++ {
		if _, err := h.host.Call(ctx, "conf", "count", nil, time.Second); !errors.Is(err, context.Canceled) {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if info, _ := h.host.Get("conf"); info.Status != model.PluginActive {
		t.Fatalf("status %s after cancelled callers", info.Status)
	}
	h.mustCall("conf", "count", nil)
}

// A paused plugin stays paused when the panel restarts: a pause is lifted by hand.
func TestPausedStaysPausedAcrossRestart(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	for i := 0; i < breakerLimit; i++ {
		_, _ = h.call("conf", "boom", nil)
	}
	if info, _ := h.host.Get("conf"); info.Status != model.PluginPaused {
		t.Fatalf("not paused: %s", info.Status)
	}
	again := New(Deps{Store: h.store, DataDir: h.dir, PanelVersion: "4.4.0", Logger: slog.New(slog.DiscardHandler)})
	t.Cleanup(func() { _ = again.Close(context.Background()) })
	if err := again.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if info, _ := again.Get("conf"); info.Status != model.PluginPaused {
		t.Fatalf("after a restart: %s", info.Status)
	}
}

// A plugin's call that reaches the same plugin again is refused, not deadlocked.
func TestReentryRefused(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	ctx := withCalling(context.Background(), "conf")
	start := time.Now()
	if _, err := h.host.Call(ctx, "conf", "count", nil, 5*time.Second); !errors.Is(err, ErrReentry) {
		t.Fatalf("re-entry: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("re-entry waited")
	}
}

// A call into a plugin that was uninstalled (or a host closed) while it queued
// does not start the plugin again.
func TestNoCallAfterUninstall(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	inst, _ := h.host.get("conf")
	if err := h.host.Uninstall(context.Background(), "conf", false); err != nil {
		t.Fatal(err)
	}
	inst.mu.Lock()
	status, vm := inst.status, inst.vm
	inst.mu.Unlock()
	if status == model.PluginActive || vm != nil {
		t.Fatalf("after uninstall: status %s, vm %v", status, vm != nil)
	}
}

// Rollback restores the snapshot of the update it undoes, never an older one: an
// update of a switched-off plugin takes its snapshot too.
func TestRollbackUsesItsOwnSnapshot(t *testing.T) {
	h := newHarness(t)
	installConf(h)
	ctx := context.Background()
	write := func(n int) {
		for i := 0; i < n; i++ {
			h.mustCall("conf", "db", nil)
		}
	}
	rows := func() string {
		out, err := h.host.DevOp(ctx, "conf", "db.query", []byte(`{"sql":"SELECT count(*) AS n FROM notes","args":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	update := func(v string) {
		man := strings.Replace(confManifest, `"version": "1.0.0"`, `"version": "`+v+`"`, 1)
		raw := pkg(t, man, confMain, confExtra)
		if _, err := h.host.Update(ctx, raw, consentFor(t, h.host, raw)); err != nil {
			t.Fatal(err)
		}
	}
	update("2.0.0") // snapshot S1 (an old one)
	write(3)
	if _, err := h.host.Disable(ctx, "conf"); err != nil {
		t.Fatal(err)
	}
	update("3.0.0") // switched off: must still snapshot, replacing S1
	if _, err := h.host.Enable(ctx, "conf"); err != nil {
		t.Fatal(err)
	}
	want := rows()
	write(2)
	if _, err := h.host.Rollback(ctx, "conf"); err != nil {
		t.Fatal(err)
	}
	if got := rows(); got != want {
		t.Fatalf("rollback restored %s, want the data as 3.0.0 found it: %s", got, want)
	}
	if _, err := os.Stat(h.host.dbPath("conf") + ".prev"); !os.IsNotExist(err) {
		t.Fatal("the used snapshot was left to stand in for a later one")
	}
}
