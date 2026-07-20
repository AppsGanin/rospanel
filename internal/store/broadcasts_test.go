package store

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

func bcStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "bc.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newBroadcast(t *testing.T, st *Store, chats ...int64) int64 {
	t.Helper()
	id, err := st.CreateBroadcast(&model.Broadcast{
		Text:     "привет",
		Audience: model.AudienceAll,
		Buttons:  []model.BroadcastButton{{Text: "Сайт", URL: "https://example.com"}},
	}, 1700000000)
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	if err := st.AddBroadcastTargets(id, chats); err != nil {
		t.Fatalf("AddBroadcastTargets: %v", err)
	}
	return id
}

// A broadcast is created paused so the caller can finish setting it up — an
// attachment is written to disk under the id this call returns, and a worker seeing
// the row as running in between would find no file.
func TestCreateBroadcastStartsPaused(t *testing.T) {
	st := bcStore(t)
	id := newBroadcast(t, st, 1, 2, 3)

	b, err := st.GetBroadcast(id)
	if err != nil {
		t.Fatalf("GetBroadcast: %v", err)
	}
	if b.Status != model.BroadcastPaused || b.StartedAt != 0 {
		t.Fatalf("expected a paused, unstarted broadcast: %+v", b)
	}
	if b.Total != 3 || b.Sent != 0 || b.Pending() != 3 {
		t.Fatalf("counts wrong: %+v", b)
	}
	if len(b.Buttons) != 1 || b.Buttons[0].URL != "https://example.com" {
		t.Fatalf("buttons not round-tripped: %+v", b.Buttons)
	}
	if got, err := st.NextRunningBroadcast(); err != nil || got != nil {
		t.Fatalf("a paused broadcast must not be picked up: %+v, %v", got, err)
	}

	if err := st.SetBroadcastStatus(id, model.BroadcastRunning, 1700000500); err != nil {
		t.Fatalf("start: %v", err)
	}
	got, err := st.NextRunningBroadcast()
	if err != nil || got == nil || got.ID != id {
		t.Fatalf("NextRunningBroadcast = %+v, %v", got, err)
	}
	if got.StartedAt != 1700000500 {
		t.Fatalf("started_at = %d", got.StartedAt)
	}
	// Resuming must not rewrite the launch time.
	if err := st.SetBroadcastStatus(id, model.BroadcastRunning, 1700009999); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if b, _ := st.GetBroadcast(id); b.StartedAt != 1700000500 {
		t.Fatalf("resume overwrote started_at: %d", b.StartedAt)
	}
}

// The primary key on (broadcast_id, chat_id) is what makes a resumed run unable to
// send twice, whatever the worker does.
func TestBroadcastTargetsAreUnique(t *testing.T) {
	st := bcStore(t)
	id := newBroadcast(t, st, 1, 2, 2, 3, 1)

	b, err := st.GetBroadcast(id)
	if err != nil {
		t.Fatalf("GetBroadcast: %v", err)
	}
	if b.Total != 3 {
		t.Fatalf("total = %d, want 3 (duplicates collapsed)", b.Total)
	}
	// Re-adding the same audience later must not resurrect anyone already delivered.
	if err := st.MarkTarget(id, 1, model.TargetSent, "", 1700000100); err != nil {
		t.Fatalf("MarkTarget: %v", err)
	}
	if err := st.AddBroadcastTargets(id, []int64{1, 2, 3}); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if b, _ = st.GetBroadcast(id); b.Sent != 1 || b.Total != 3 {
		t.Fatalf("re-adding disturbed progress: %+v", b)
	}
}

func TestBroadcastProgress(t *testing.T) {
	st := bcStore(t)
	id := newBroadcast(t, st, 1, 2, 3, 4)

	if err := st.MarkTarget(id, 1, model.TargetSent, "", 1700000100); err != nil {
		t.Fatalf("sent: %v", err)
	}
	if err := st.MarkTarget(id, 2, model.TargetFailed, "boom", 1700000100); err != nil {
		t.Fatalf("failed: %v", err)
	}
	if err := st.MarkTarget(id, 3, model.TargetBlocked, "403", 1700000100); err != nil {
		t.Fatalf("blocked: %v", err)
	}
	b, err := st.GetBroadcast(id)
	if err != nil {
		t.Fatalf("GetBroadcast: %v", err)
	}
	if b.Total != 4 || b.Sent != 1 || b.Failed != 1 || b.Blocked != 1 || b.Pending() != 1 {
		t.Fatalf("progress wrong: %+v", b)
	}

	// Only the untouched recipient is still queued, so a resume can't repeat anyone.
	pending, err := st.NextPendingTargets(id, 10)
	if err != nil {
		t.Fatalf("NextPendingTargets: %v", err)
	}
	if len(pending) != 1 || pending[0] != 4 {
		t.Fatalf("pending = %v, want [4]", pending)
	}
}

// Retry re-queues transient failures only. A blocked chat will be refused again for
// exactly the same reason, so retrying it just spends a send slot.
func TestRetryFailedOnly(t *testing.T) {
	st := bcStore(t)
	id := newBroadcast(t, st, 1, 2, 3)
	for _, c := range []struct {
		chat  int64
		state string
	}{{1, model.TargetSent}, {2, model.TargetFailed}, {3, model.TargetBlocked}} {
		if err := st.MarkTarget(id, c.chat, c.state, "", 1700000100); err != nil {
			t.Fatalf("mark %d: %v", c.chat, err)
		}
	}
	if err := st.SetBroadcastStatus(id, model.BroadcastDone, 1700000200); err != nil {
		t.Fatalf("finish: %v", err)
	}

	n, err := st.RetryFailedBroadcast(id, 1700000300)
	if err != nil || n != 1 {
		t.Fatalf("RetryFailedBroadcast = %d, %v; want 1", n, err)
	}
	b, err := st.GetBroadcast(id)
	if err != nil {
		t.Fatalf("GetBroadcast: %v", err)
	}
	if b.Status != model.BroadcastRunning || b.FinishedAt != 0 {
		t.Fatalf("retry did not reopen the run: %+v", b)
	}
	if b.Sent != 1 || b.Blocked != 1 || b.Failed != 0 || b.Pending() != 1 {
		t.Fatalf("retry touched the wrong rows: %+v", b)
	}

	// Nothing left to retry reports zero rather than reopening a finished run.
	if err := st.MarkTarget(id, 2, model.TargetSent, "", 1700000400); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := st.SetBroadcastStatus(id, model.BroadcastDone, 1700000500); err != nil {
		t.Fatalf("finish again: %v", err)
	}
	if n, err = st.RetryFailedBroadcast(id, 1700000600); err != nil || n != 0 {
		t.Fatalf("second retry = %d, %v; want 0", n, err)
	}
	if b, _ = st.GetBroadcast(id); b.Status != model.BroadcastDone {
		t.Fatalf("an empty retry reopened the run: %+v", b)
	}
}

func TestListBroadcastsNewestFirst(t *testing.T) {
	st := bcStore(t)
	first := newBroadcast(t, st, 1)
	second := newBroadcast(t, st, 2, 3)

	list, err := st.ListBroadcasts(10)
	if err != nil {
		t.Fatalf("ListBroadcasts: %v", err)
	}
	if len(list) != 2 || list[0].ID != second || list[1].ID != first {
		t.Fatalf("order wrong: %+v", list)
	}
	if list[0].Total != 2 || list[1].Total != 1 {
		t.Fatalf("per-row counts wrong: %+v", list)
	}
}

// Deleting a broadcast must take its recipient rows with it, or the table grows
// without bound and orphan rows skew nothing but still sit there.
func TestBroadcastTargetsCascade(t *testing.T) {
	st := bcStore(t)
	id := newBroadcast(t, st, 1, 2, 3)
	if _, err := st.db.Exec(`DELETE FROM broadcasts WHERE id = ?`, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var n int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM broadcast_targets WHERE broadcast_id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d orphan target rows survived", n)
	}
}
