package core

import (
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestAdminSessionsPermissions(t *testing.T) {
	m, ownerID := rosterManager(t)

	// Create admin and operator
	admin1, err := m.CreateAdmin("admin1", "pass12345", model.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin1: %v", err)
	}
	admin2, err := m.CreateAdmin("admin2", "pass12345", model.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin2: %v", err)
	}
	op1, err := m.CreateAdmin("operator1", "pass12345", model.RoleOperator)
	if err != nil {
		t.Fatalf("create op1: %v", err)
	}

	// Create sessions
	ownerTok, err := m.store.CreateSession(ownerID, time.Hour, "1.1.1.1", "OwnerDevice")
	if err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	admin1Tok, err := m.store.CreateSession(admin1.ID, time.Hour, "2.2.2.2", "Admin1Device")
	if err != nil {
		t.Fatalf("create admin1 session: %v", err)
	}
	admin2Tok, err := m.store.CreateSession(admin2.ID, time.Hour, "3.3.3.3", "Admin2Device")
	if err != nil {
		t.Fatalf("create admin2 session: %v", err)
	}
	op1Tok, err := m.store.CreateSession(op1.ID, time.Hour, "4.4.4.4", "Op1Device")
	if err != nil {
		t.Fatalf("create op1 session: %v", err)
	}

	ownerHash, _ := m.store.TokenHash(ownerTok)
	admin1Hash, _ := m.store.TokenHash(admin1Tok)
	admin2Hash, _ := m.store.TokenHash(admin2Tok)
	op1Hash, _ := m.store.TokenHash(op1Tok)

	// 1. Operator tests
	// Operator can list own sessions
	opSessions, err := m.ListAdminSessions(op1.ID, op1.ID)
	if err != nil || len(opSessions) != 1 {
		t.Fatalf("op list own: err=%v len=%d", err, len(opSessions))
	}
	// Operator cannot list admin sessions
	if _, err := m.ListAdminSessions(op1.ID, admin1.ID); err == nil {
		t.Error("operator listed admin sessions, want error")
	}
	// Operator cannot delete admin session
	if err := m.DeleteAdminSession(op1.ID, admin1.ID, admin1Hash); err == nil {
		t.Error("operator deleted admin session, want error")
	}
	// Operator can delete own session
	if err := m.DeleteAdminSession(op1.ID, op1.ID, op1Hash); err != nil {
		t.Fatalf("operator delete own session failed: %v", err)
	}

	// 2. Admin tests
	// Admin can list own sessions
	adminSessions, err := m.ListAdminSessions(admin1.ID, admin1.ID)
	if err != nil || len(adminSessions) != 1 {
		t.Fatalf("admin list own: err=%v len=%d", err, len(adminSessions))
	}
	// Re-create op session
	op1Tok2, _ := m.store.CreateSession(op1.ID, time.Hour, "4.4.4.5", "Op1Device2")
	op1Hash2, _ := m.store.TokenHash(op1Tok2)

	// Admin can list operator sessions
	opSessionsFromAdmin, err := m.ListAdminSessions(admin1.ID, op1.ID)
	if err != nil || len(opSessionsFromAdmin) != 1 {
		t.Fatalf("admin list op sessions: err=%v len=%d", err, len(opSessionsFromAdmin))
	}
	// Admin CANNOT list other admin sessions
	if _, err := m.ListAdminSessions(admin1.ID, admin2.ID); err == nil {
		t.Error("admin listed another admin sessions, want error")
	}
	// Admin CANNOT list owner sessions
	if _, err := m.ListAdminSessions(admin1.ID, ownerID); err == nil {
		t.Error("admin listed owner sessions, want error")
	}
	// Admin CANNOT delete other admin session
	if err := m.DeleteAdminSession(admin1.ID, admin2.ID, admin2Hash); err == nil {
		t.Error("admin deleted another admin session, want error")
	}
	// Admin CANNOT delete owner session
	if err := m.DeleteAdminSession(admin1.ID, ownerID, ownerHash); err == nil {
		t.Error("admin deleted owner session, want error")
	}
	// Admin CAN delete operator session
	if err := m.DeleteAdminSession(admin1.ID, op1.ID, op1Hash2); err != nil {
		t.Fatalf("admin failed to delete operator session: %v", err)
	}
	// Admin CAN delete own session
	if err := m.DeleteAdminSession(admin1.ID, admin1.ID, admin1Hash); err != nil {
		t.Fatalf("admin failed to delete own session: %v", err)
	}

	// 3. Owner tests
	// Owner can list all sessions
	allSessions, err := m.ListAdminSessions(ownerID, 0)
	if err != nil {
		t.Fatalf("owner list all: %v", err)
	}
	if len(allSessions) == 0 {
		t.Fatal("owner saw 0 sessions")
	}
	// Owner CAN delete any admin session
	if err := m.DeleteAdminSession(ownerID, admin2.ID, admin2Hash); err != nil {
		t.Fatalf("owner failed to delete admin2 session: %v", err)
	}
	// Owner CAN delete own session
	if err := m.DeleteAdminSession(ownerID, ownerID, ownerHash); err != nil {
		t.Fatalf("owner failed to delete own session: %v", err)
	}
}
