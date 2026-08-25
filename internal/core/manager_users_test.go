package core

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// A bulk action is reachable from the panel, /v1 and the post_users_bulk MCP tool, and
// the id list arrives straight off a JSON decode with no bound anywhere upstream. Every
// id costs a row read, and a delete costs a webhook delivery per subscriber on a queue
// that drops when full — so an unbounded list silently loses the notifications it
// generates while holding the single DB connection.
func TestBulkUserActionIsBounded(t *testing.T) {
	// A real store, because the point is that the BOUND refuses the call — with a bare
	// Manager the nil store errors on its own and the test would pass either way. (It
	// did, the first time I wrote it.)
	st, err := store.Open(filepath.Join(t.TempDir(), "bulk.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st}

	u, err := st.CreateUser("bulk-one", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// An ordinary selection goes through: the bound must not refuse real work.
	if _, err := m.BulkUserAction(context.Background(), []int64{u.ID}, "disable", 0); err != nil {
		t.Fatalf("an ordinary bulk action was refused: %v", err)
	}

	ids := make([]int64, maxBulkUsers+1)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	_, err = m.BulkUserAction(context.Background(), ids, "disable", 0)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Code != "err.tooManyUsersSelected" {
		t.Errorf("%d ids were accepted (or refused for the wrong reason): %v", len(ids), err)
	}
}

func TestSetUserLimitsDeviceBounds(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "userlimits.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st}

	u, err := st.CreateUser("user-dev", "uuid", "pw", "tok", 0, 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	for _, lim := range []int{0, 1, 7, 25, model.MaxDevicesPerUser} {
		if err := m.SetUserLimits(context.Background(), u.ID, 0, 0, lim); err != nil {
			t.Fatalf("set device_limit=%d failed: %v", lim, err)
		}
	}

	if err := m.SetUserLimits(context.Background(), u.ID, 0, 0, -1); err == nil {
		t.Fatal("negative device_limit must be rejected")
	}

	if err := m.SetUserLimits(context.Background(), u.ID, 0, 0, model.MaxDevicesPerUser+1); err == nil {
		t.Fatal("device_limit > MaxDevicesPerUser must be rejected")
	}
}
