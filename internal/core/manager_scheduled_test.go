package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A scheduled broadcast waits with no recipients, can only be called off meanwhile,
// and reads its audience when it goes out.
func TestScheduledBroadcast(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	ctx := context.Background()
	now := time.Now().Unix()
	if _, err := m.CreateBroadcast(ctx, &model.Broadcast{Text: "later", ScheduledAt: now + 2*broadcastScheduleMax}); validationCode(err) != "err.broadcastTooFar" {
		t.Errorf("scheduled two years ahead: %v", err)
	}
	b, err := m.CreateBroadcast(ctx, &model.Broadcast{Text: "later", ScheduledAt: now + 3600})
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != model.BroadcastScheduled || b.Total != 0 || b.ScheduledAt != now+3600 {
		t.Fatalf("created %+v", b)
	}
	if err := m.SetBroadcastStatus(b.ID, model.BroadcastRunning); validationCode(err) != "err.broadcastScheduled" {
		t.Errorf("a scheduled broadcast resumed: %v", err)
	}
	// Someone who arrives before it goes out is in its audience.
	subscribeChat(t, m, 77, 0)
	m.StartDueBroadcasts(now) // not yet
	if got, _ := m.store.GetBroadcast(b.ID); got.Status != model.BroadcastScheduled {
		t.Fatalf("started early: %s", got.Status)
	}
	m.StartDueBroadcasts(now + 3600)
	got, _ := m.store.GetBroadcast(b.ID)
	if got.Status != model.BroadcastRunning || got.Total != 1 {
		t.Fatalf("at its time: status %s, total %d", got.Status, got.Total)
	}

	// Cancelled while waiting: it never goes out.
	c, err := m.CreateBroadcast(ctx, &model.Broadcast{Text: "never", ScheduledAt: now + 60})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetBroadcastStatus(c.ID, model.BroadcastCancelled); err != nil {
		t.Fatal(err)
	}
	m.StartDueBroadcasts(now + 120)
	if got, _ := m.store.GetBroadcast(c.ID); got.Status != model.BroadcastCancelled || got.Total != 0 {
		t.Errorf("a cancelled scheduled broadcast: %s, total %d", got.Status, got.Total)
	}

	// Cancelled after the runner claimed it, before it started: the cancel stands.
	late, err := m.CreateBroadcast(ctx, &model.Broadcast{Text: "racy", ScheduledAt: now + 60})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.store.SetBroadcastStatusIf(late.ID, model.BroadcastScheduled, model.BroadcastPaused, 0); !ok {
		t.Fatal("claim failed")
	}
	if err := m.SetBroadcastStatus(late.ID, model.BroadcastCancelled); err != nil {
		t.Fatal(err)
	}
	if err := m.StartBroadcast(late.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.store.GetBroadcast(late.ID); got.Status != model.BroadcastCancelled {
		t.Errorf("a cancel after the claim was overwritten: %s", got.Status)
	}

	// Due more than a day ago (the panel was down): cancelled, not sent late.
	stale, err := m.CreateBroadcast(ctx, &model.Broadcast{Text: "stale", ScheduledAt: now + 60})
	if err != nil {
		t.Fatal(err)
	}
	m.StartDueBroadcasts(now + 60 + broadcastLateMax + 1)
	if got, _ := m.store.GetBroadcast(stale.ID); got.Status != model.BroadcastCancelled {
		t.Errorf("a day-late broadcast: %s", got.Status)
	}

	// A time already past is refused, not sent at once behind the operator's back.
	if _, err := m.CreateBroadcast(ctx, &model.Broadcast{Text: "late", ScheduledAt: now - 10}); validationCode(err) != "err.broadcastInPast" {
		t.Errorf("a past time: %v", err)
	}
}

// Nobody left to send to when its time comes: cancelled, not stuck.
func TestScheduledBroadcastWithNobody(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	now := time.Now().Unix()
	b, err := m.CreateBroadcast(context.Background(), &model.Broadcast{Text: "x", ScheduledAt: now + 60})
	if err != nil {
		t.Fatal(err)
	}
	m.StartDueBroadcasts(now + 60)
	if got, _ := m.store.GetBroadcast(b.ID); got.Status != model.BroadcastCancelled {
		t.Errorf("status %s, want cancelled", got.Status)
	}
}

// Maintenance under way that asks for it takes its servers out of subscriptions;
// what is ahead, over, or did not ask does not.
func TestMaintenanceHiddenServers(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	nl := joinedNode(t, m, "NL")
	de := joinedNode(t, m, "DE")
	now := time.Now().Unix()
	for _, in := range []model.Incident{
		{Kind: model.IncidentKindMaintenance, Title: "nl now", NodeIDs: []int64{nl}, StartsAt: now - 60, EndsAt: now + 3600, HideInSub: true},
		{Kind: model.IncidentKindMaintenance, Title: "de later", NodeIDs: []int64{de}, StartsAt: now + 3600, HideInSub: true},
		{Kind: model.IncidentKindMaintenance, Title: "main, kept", NodeIDs: []int64{model.LocalNodeID}, StartsAt: now - 60},
		{Kind: model.IncidentKindIncident, Title: "de broken", NodeIDs: []int64{de}, HideInSub: true},
	} {
		saved, err := m.SaveIncident(in)
		if err != nil {
			t.Fatal(err)
		}
		if in.Kind == model.IncidentKindIncident && saved.HideInSub {
			t.Error("an incident kept HideInSub")
		}
	}
	got := m.MaintenanceHiddenServers()
	if len(got) != 1 || !got[nl] {
		t.Errorf("hidden %v, want only %d", got, nl)
	}
}

// The month is summed up in the admin bot's language, once, on the 1st from 9:00 —
// or by the 3rd, the panel having been off.
func TestMonthlyReport(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	m.tz = time.UTC
	var sent []string
	m.SetAdminNotifier(func(html string) { sent = append(sent, html) })
	// No admin bot to deliver it yet: it waits rather than being marked sent.
	m.maybeMonthlyReport(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	if set, _ := m.store.GetSettings(); set.MonthlyReportSent != "" {
		t.Fatalf("marked sent with no admin bot: %q", set.MonthlyReportSent)
	}
	if err := m.store.ExecForTest(`UPDATE settings SET tg_bot_enabled = 1, tg_bot_token = '222:BBB', tg_chat_ids = '555'`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.CreateUser("ann", "uuid-ann", "pw", "tok-ann", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.store.ExecForTest(`UPDATE users SET created_at = ?`, time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := m.store.RecordUptimeSample(model.LocalNodeID, "2026-09-15", true); err != nil {
		t.Fatal(err)
	}

	m.maybeMonthlyReport(time.Date(2026, 10, 1, 8, 59, 0, 0, time.UTC)) // before 9:00
	m.maybeMonthlyReport(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)) // too late
	if len(sent) != 0 {
		t.Fatalf("sent outside its window: %q", sent)
	}
	m.maybeMonthlyReport(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	m.maybeMonthlyReport(time.Date(2026, 10, 1, 9, 1, 0, 0, time.UTC)) // once
	if len(sent) != 1 {
		t.Fatalf("sent %d times", len(sent))
	}
	for _, want := range []string{"сентябрь 2026", "Новых пользователей: 1", "Master 100.00%"} {
		if !strings.Contains(sent[0], want) && !strings.Contains(sent[0], strings.Replace(want, "Master", model.LocalNodeName, 1)) {
			t.Errorf("summary lacks %q:\n%s", want, sent[0])
		}
	}
	if strings.Contains(sent[0], "Доход") {
		t.Error("revenue shown with billing off and nothing paid")
	}
	if set, _ := m.store.GetSettings(); set.MonthlyReportSent != "2026-09" {
		t.Errorf("recorded month %q", set.MonthlyReportSent)
	}
}
