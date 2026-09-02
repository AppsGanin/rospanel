package core

import (
	"context"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

func TestAdminLoginIsNewReadsTheAuditTrail(t *testing.T) {
	m := nodeTestManager(t)
	if !m.AdminLoginIsNew("owner", "203.0.113.7") {
		t.Fatal("an address never seen must be new")
	}
	m.AddAdminAudit(model.AdminAudit{Action: model.AuditLogin, ActorKind: model.ActorAdmin,
		ActorName: "owner", Target: "owner", IP: "203.0.113.7"})
	if m.AdminLoginIsNew("owner", "203.0.113.7") {
		t.Fatal("an address signed in from before must be known")
	}
	// Known to one admin, not to another; a failed attempt from it counts for nothing.
	if !m.AdminLoginIsNew("second", "203.0.113.7") {
		t.Fatal("history is per admin")
	}
	m.AddAdminAudit(model.AdminAudit{Action: model.AuditLoginFailed, ActorKind: model.ActorAdmin,
		ActorName: "second", Target: "second", IP: "203.0.113.8"})
	if !m.AdminLoginIsNew("second", "203.0.113.8") {
		t.Fatal("a failed attempt must not make an address known")
	}
	// Beyond the retention window the memory is gone — the row would be too.
	m.AddAdminAudit(model.AdminAudit{Action: model.AuditLogin, ActorKind: model.ActorAdmin,
		ActorName: "owner", Target: "owner", IP: "203.0.113.9",
		CreatedAt: time.Now().AddDate(0, 0, -model.AdminAuditRetentionDays-1).Unix()})
	if !m.AdminLoginIsNew("owner", "203.0.113.9") {
		t.Fatal("a sign-in older than the retention window must read as new")
	}
}

func TestNotifyAdminLoginIsGatedByTheCategory(t *testing.T) {
	m := nodeTestManager(t)
	var got []LoginAlert
	m.SetAdminLoginNotifier(func(a LoginAlert) { got = append(got, a) })

	m.NotifyAdminLogin(LoginAlert{AdminID: 1, Username: "owner", IP: "203.0.113.7", Client: "Chrome · macOS"})
	if len(got) != 1 || got[0].At == 0 {
		t.Fatalf("default (all categories on): want one stamped alert, got %+v", got)
	}

	// Switch the category off and nothing reaches the bot.
	if err := m.SaveAdminEventPrefs(map[string]bool{"abuse": true}); err != nil {
		t.Fatal(err)
	}
	m.NotifyAdminLogin(LoginAlert{AdminID: 1, Username: "owner", IP: "203.0.113.7"})
	if len(got) != 1 {
		t.Fatalf("category off: alert still delivered (%d)", len(got))
	}
	// No bot at all: no panic, no delivery.
	m.SetAdminLoginNotifier(nil)
	m.NotifyAdminLogin(LoginAlert{AdminID: 1})
}

func TestRevokeAdminSessionsEndsThemAllAndSaysWho(t *testing.T) {
	m := nodeTestManager(t)
	id, err := m.store.CreateAdmin("owner", "x", model.RoleOwner, false)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := m.store.CreateSessionFrom(id, time.Hour, "203.0.113.7", "ua"); err != nil {
			t.Fatal(err)
		}
	}
	ctx := actor.With(context.Background(), actor.Telegram("@owner"))
	name, n, err := m.RevokeAdminSessions(ctx, id)
	if err != nil || name != "owner" || n != 3 {
		t.Fatalf("RevokeAdminSessions = %q, %d, %v; want owner, 3, nil", name, n, err)
	}
	left, err := m.store.ListAdminSessions(id)
	if err != nil || len(left) != 0 {
		t.Fatalf("sessions left: %d (%v)", len(left), err)
	}
	rows, err := m.store.ListAdminAudit(store.AdminAuditFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.Action == model.AuditSessionRevoked && r.Target == "owner" && r.ActorKind == model.ActorTelegram && r.ActorName == "@owner" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no audit row naming the Telegram actor: %+v", rows)
	}
	if _, _, err := m.RevokeAdminSessions(ctx, id+100); err == nil {
		t.Fatal("an unknown admin must be an error, not a silent no-op")
	}
}

func TestClientLabel(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36": "Chrome · macOS",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1": "Safari · iPhone",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36 Edg/126.0": "Edge · Windows",
		"Mozilla/5.0 (X11; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0":                                                "Firefox · Linux",
		"curl/8.6.0": "curl",
		"":           "",
	}
	for ua, want := range cases {
		if got := ClientLabel(ua); got != want {
			t.Errorf("ClientLabel(%q) = %q, want %q", ua, got, want)
		}
	}
}
