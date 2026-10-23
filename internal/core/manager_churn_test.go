package core

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A user whose paid term lapsed is asked once, a day later; the answer is kept once
// per lapse, "other" takes words after the button, and the page stops asking.
func TestChurnSurvey(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	ctx := context.Background()
	gone := walletUser(t, st, "gone")
	never := walletUser(t, st, "never-paid")
	paidOrder(t, m, gone.ID, plan.ID)
	if err := st.ExecForTest(`UPDATE users SET tg_chat_id = 4242 WHERE id = ?`, gone.ID); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var asked []int64
	m.SetUserSurveyor(func(chatID int64) {
		mu.Lock()
		asked = append(asked, chatID)
		mu.Unlock()
	})
	// The question goes out off the sweep; wait for what the sweep sent.
	askedNow := func(want int) []int64 {
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			got := slices.Clone(asked)
			mu.Unlock()
			if len(got) >= want || time.Now().After(deadline) {
				return got
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Off: nothing is asked, nothing can be answered.
	set, _ := st.GetSettings()
	if err := m.AnswerChurn(ctx, gone.ID, model.ChurnPrice, "", "page"); validationCode(err) != "err.churnClosed" {
		t.Fatalf("answer while off: %v", err)
	}
	set.ChurnSurvey = true
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelUserPlan(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	set, _ = st.GetSettings()
	set.TGUserBotEnabled = true

	m.runChurnSurvey(set, now+3600) // too early
	if got := askedNow(0); len(got) != 0 {
		t.Fatalf("asked %v before a day passed", got)
	}
	m.surveyAt.Store(0)
	m.runChurnSurvey(set, now+2*86400)
	if got := askedNow(1); len(got) != 1 || got[0] != 4242 {
		t.Fatalf("asked %v, want the one chat", got)
	}
	m.surveyAt.Store(0)
	m.runChurnSurvey(set, now+2*86400+60)
	time.Sleep(100 * time.Millisecond)
	if got := askedNow(1); len(got) != 1 {
		t.Fatalf("asked %v, want the one chat once", got)
	}

	u, _ := st.GetUser(gone.ID)
	if !m.ChurnSurveyOpen(*u) {
		t.Fatal("the page does not ask a lapsed user")
	}
	if n, _ := st.GetUser(never.ID); m.ChurnSurveyOpen(*n) {
		t.Fatal("the page asks a user who never paid")
	}
	if err := m.AnswerChurn(ctx, gone.ID, "cheaper", "", "bot"); validationCode(err) != "err.churnReason" {
		t.Fatalf("unknown reason: %v", err)
	}
	if err := m.AnswerChurn(ctx, gone.ID, model.ChurnOther, "", "bot"); err != nil {
		t.Fatal(err)
	}
	if err := m.AnswerChurn(ctx, gone.ID, model.ChurnOther, "  too slow at night ", "bot"); err != nil {
		t.Fatal(err)
	}
	if m.ChurnSurveyOpen(*u) {
		t.Fatal("the page asks again after an answer")
	}
	stats, err := m.ChurnStats(0)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 1 || stats.Reasons[model.ChurnOther] != 1 || len(stats.Comments) != 1 ||
		stats.Comments[0].Comment != "too slow at night" || stats.Comments[0].UserName != "gone" {
		t.Fatalf("stats = %+v", stats)
	}

	// The same departure ending again (a free plan's own term running out) is not
	// asked about twice: a new survey waits for a payment.
	later := time.Now().Unix() - 10
	if err := st.ExecForTest(`UPDATE users SET expire_at = ? WHERE id = ?`, later, gone.ID); err != nil {
		t.Fatal(err)
	}
	if u, _ = st.GetUser(gone.ID); m.ChurnSurveyOpen(*u) {
		t.Fatal("the page asks again about the same departure")
	}
	if c, _ := st.SurveyCandidates(0, time.Now().Unix()+86400, 10); len(c) != 0 {
		t.Fatalf("the bot would ask again: %+v", c)
	}
	// A term a refund ended is no departure at all.
	// Without a free plan to fall to: the term is ended outright.
	refunded := walletUser(t, st, "refunded")
	paidOrder(t, m, refunded.ID, plan.ID)
	set, _ = st.GetSettings()
	free := set.BillingFreePlanID
	set.BillingFreePlanID = 0
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	if err := m.cancelUserPlan(ctx, refunded.ID, false); err != nil {
		t.Fatal(err)
	}
	set.BillingFreePlanID = free
	if err := m.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	if r, _ := st.GetUser(refunded.ID); m.ChurnSurveyOpen(*r) {
		t.Fatal("the page asks a user whose term a refund ended")
	}
	if c, _ := st.SurveyCandidates(0, time.Now().Unix()+86400, 10); len(c) != 0 {
		t.Fatalf("the bot would ask after a refund: %+v", c)
	}

	// Back on a plan: nothing more to answer.
	paidOrder(t, m, gone.ID, plan.ID)
	if err := m.AnswerChurn(ctx, gone.ID, model.ChurnPrice, "", "page"); validationCode(err) != "err.churnClosed" {
		t.Fatalf("answer while subscribed: %v", err)
	}
}
