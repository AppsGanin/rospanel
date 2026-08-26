package store

import (
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestSessionStoreCRUD(t *testing.T) {
	st := newStore(t)

	admin1, err := st.CreateAdmin("admin1", "h", model.RoleAdmin, false)
	if err != nil {
		t.Fatalf("create admin1: %v", err)
	}
	op1, err := st.CreateAdmin("op1", "h", model.RoleOperator, false)
	if err != nil {
		t.Fatalf("create op1: %v", err)
	}

	tok1, err := st.CreateSession(admin1, time.Hour, "1.2.3.4", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("create session 1: %v", err)
	}
	tok2, err := st.CreateSession(admin1, time.Hour, "5.6.7.8", "Chrome/100")
	if err != nil {
		t.Fatalf("create session 2: %v", err)
	}
	tok3, err := st.CreateSession(op1, time.Hour, "9.9.9.9", "Safari/16")
	if err != nil {
		t.Fatalf("create session 3: %v", err)
	}
	if _, ok := st.LookupSession(tok3); !ok {
		t.Fatal("session 3 does not resolve")
	}

	// List sessions for admin1
	s1, err := st.ListSessions(admin1)
	if err != nil {
		t.Fatalf("list admin1: %v", err)
	}
	if len(s1) != 2 {
		t.Fatalf("admin1 sessions count = %d, want 2", len(s1))
	}
	if s1[0].AdminID != admin1 || s1[0].Username != "admin1" {
		t.Errorf("unexpected session data: %+v", s1[0])
	}

	// List sessions for op1
	sOp, err := st.ListSessions(op1)
	if err != nil {
		t.Fatalf("list op1: %v", err)
	}
	if len(sOp) != 1 {
		t.Fatalf("op1 sessions count = %d, want 1", len(sOp))
	}
	if sOp[0].IP != "9.9.9.9" || sOp[0].UserAgent != "Safari/16" {
		t.Errorf("unexpected op session metadata: %+v", sOp[0])
	}

	// List all sessions (adminID = 0)
	all, err := st.ListSessions(0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all sessions count = %d, want 3", len(all))
	}

	// Get session by hash
	hash1, err := st.TokenHash(tok1)
	if err != nil {
		t.Fatalf("tokenHash: %v", err)
	}
	got1, err := st.GetSessionByHash(hash1)
	if err != nil {
		t.Fatalf("get by hash: %v", err)
	}
	if got1.TokenHash != hash1 || got1.IP != "1.2.3.4" {
		t.Errorf("got session = %+v, want hash %s", got1, hash1)
	}

	// Delete single session by hash
	if err := st.DeleteSessionByHash(hash1); err != nil {
		t.Fatalf("delete by hash: %v", err)
	}
	if _, ok := st.LookupSession(tok1); ok {
		t.Error("deleted session still resolves")
	}
	if _, err := st.GetSessionByHash(hash1); err == nil {
		t.Error("get deleted session succeeded, want error")
	}

	// Delete all other sessions for admin1 except tok2
	tok4, err := st.CreateSession(admin1, time.Hour, "1.1.1.1", "curl/8.0")
	if err != nil {
		t.Fatalf("create session 4: %v", err)
	}
	if err := st.DeleteSessionsForAdminExcept(admin1, tok2); err != nil {
		t.Fatalf("delete except: %v", err)
	}
	if _, ok := st.LookupSession(tok4); ok {
		t.Error("tok4 should have been revoked")
	}
	if _, ok := st.LookupSession(tok2); !ok {
		t.Error("tok2 should have been kept")
	}
}
