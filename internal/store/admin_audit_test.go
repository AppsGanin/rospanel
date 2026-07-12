package store

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestAdminAuditFilterAndPaging(t *testing.T) {
	st := newStore(t)

	for i := range 5 {
		action := model.AuditLogin
		if i%2 == 0 {
			action = model.AuditSettings
		}
		if err := st.AddAdminAudit(model.AdminAudit{
			Action:    action,
			ActorKind: model.ActorAdmin,
			ActorName: "owner",
			IP:        "1.2.3.4",
			Details:   map[string]any{"n": i},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	// Newest first.
	all, err := st.ListAdminAudit(AdminAuditFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("rows = %d, want 5", len(all))
	}
	if all[0].ID < all[len(all)-1].ID {
		t.Error("rows are oldest-first, want newest-first")
	}
	if all[0].IP != "1.2.3.4" || all[0].ActorName != "owner" {
		t.Errorf("row = %+v, want the actor and IP round-tripped", all[0])
	}
	if d, ok := all[0].Details.(map[string]any); !ok || d["n"] == nil {
		t.Errorf("details = %v, want the JSON object back", all[0].Details)
	}

	// Filter by one action.
	logins, err := st.ListAdminAudit(AdminAuditFilter{
		Actions: []string{model.AuditLogin}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(logins) != 2 {
		t.Fatalf("login rows = %d, want 2", len(logins))
	}

	// Filter by a whole category — what the journal's dropdown actually sends. The
	// session category holds login/login_failed/logout, so it must find the two
	// logins and nothing else.
	session, err := st.ListAdminAudit(AdminAuditFilter{
		Actions: model.AdminAuditActionsIn(model.AuditCatSession), Limit: 10,
	})
	if err != nil {
		t.Fatalf("category filter: %v", err)
	}
	if len(session) != 2 {
		t.Fatalf("session rows = %d, want the 2 logins", len(session))
	}

	// Page backwards from the oldest row of the first page.
	first, err := st.ListAdminAudit(AdminAuditFilter{Limit: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	second, err := st.ListAdminAudit(AdminAuditFilter{Limit: 2, BeforeID: first[1].ID})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(second) != 2 || second[0].ID >= first[1].ID {
		t.Errorf("page 2 = %+v, want the two rows older than %d", second, first[1].ID)
	}
}

// Retention has a habit of quietly not working: the sweep must actually delete the
// old rows and leave the recent ones alone.
func TestPurgeAdminAudit(t *testing.T) {
	st := newStore(t)

	now := time.Now()
	old := now.AddDate(0, 0, -model.AdminAuditRetentionDays-1).Unix()
	recent := now.AddDate(0, 0, -1).Unix()
	for _, ts := range []int64{old, old, recent} {
		if err := st.AddAdminAudit(model.AdminAudit{
			Action: model.AuditLogin, ActorKind: model.ActorAdmin,
			ActorName: "owner", CreatedAt: ts,
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	cutoff := now.AddDate(0, 0, -model.AdminAuditRetentionDays).Unix()
	n, err := st.PurgeAdminAudit(cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 2 {
		t.Errorf("purged %d rows, want the 2 past the window", n)
	}
	left, err := st.ListAdminAudit(AdminAuditFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(left) != 1 || left[0].CreatedAt != recent {
		t.Errorf("survivors = %+v, want only the recent row", left)
	}
}
