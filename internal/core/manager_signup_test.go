package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// signupFixture is a panel selling with a trial plan and a referral programme, and
// sign-up in the given mode.
func signupFixture(t *testing.T, mode, code string) (*Manager, *model.TariffPlan) {
	t.Helper()
	m := bulkTestManager(t)
	st := m.store
	free := &model.TariffPlan{Slug: "su-free", Name: "Free", PeriodDays: 30, DataLimit: 1 << 30, Enabled: true}
	trial := &model.TariffPlan{Slug: "su-trial", Name: "Trial", PeriodDays: 3, DataLimit: 5 << 30, Enabled: true}
	for _, p := range []*model.TariffPlan{free, trial} {
		if err := st.SaveTariffPlan(p); err != nil {
			t.Fatalf("save %s: %v", p.Slug, err)
		}
	}
	set, _ := st.GetSettings()
	set.BillingEnabled = true
	set.BillingFreePlanID = free.ID
	set.BillingTrialPlanID = trial.ID
	set.RefMode, set.RefPercent = model.RefPercent, 10
	if err := st.SetBillingSettings(set); err != nil {
		t.Fatalf("billing settings: %v", err)
	}
	if err := st.SetTelegramUserBot(false, "", mode, code); err != nil {
		t.Fatalf("registration mode: %v", err)
	}
	return m, trial
}

func errCode(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return ""
}

// A website sign-up is a bot sign-up: the trial plan, counted as a trial, with the
// source and the referrer it came with — and signing up again returns the account
// instead of a second trial.
func TestSignupGivesOneTrialPerExternalID(t *testing.T) {
	t.Parallel()
	m, trial := signupFixture(t, model.RegOpen, "")
	ctx := context.Background()
	referrer, err := m.store.CreateUser("ref", "uuid-ref", "pw", "tok-ref", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	code, err := m.RefCode(referrer.ID)
	if err != nil || code == "" {
		t.Fatalf("ref code %q: %v", code, err)
	}

	res, err := m.Signup(ctx, SignupRequest{
		ExternalID: "ann@example.com", Source: "VK_Ads", Ref: "r_" + code, IP: "203.0.113.5",
	})
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if res.Status != SignupCreated || res.User == nil {
		t.Fatalf("result = %+v", res)
	}
	u, _ := m.store.GetUser(res.User.ID)
	if u.PlanID != trial.ID || !u.TrialUsed || u.ExpireAt == 0 {
		t.Fatalf("not a trial: plan=%d trial_used=%v expire=%d", u.PlanID, u.TrialUsed, u.ExpireAt)
	}
	if u.Name != "ann@example.com" {
		t.Errorf("name = %q, want the external id", u.Name)
	}
	if got := m.store.UserExternalID(u.ID); got != "ann@example.com" {
		t.Errorf("external id = %q", got)
	}
	if got := m.store.UserSource(u.ID); got != "vk_ads" {
		t.Errorf("source = %q", got)
	}
	if w, err := m.store.GetWallet(u.ID); err != nil || w.ReferrerID != referrer.ID {
		t.Errorf("referrer = %d (%v), want %d", w.ReferrerID, err, referrer.ID)
	}

	// Again — even from elsewhere, even once sign-up has closed: the same account.
	if err := m.store.SetTelegramUserBot(false, "", model.RegOff, ""); err != nil {
		t.Fatal(err)
	}
	again, err := m.Signup(ctx, SignupRequest{ExternalID: "ann@example.com", IP: "198.51.100.9"})
	if err != nil {
		t.Fatalf("second signup: %v", err)
	}
	if again.Status != SignupExisting || again.User == nil || again.User.ID != u.ID {
		t.Fatalf("second signup = %+v, want existing user %d", again, u.ID)
	}
	if users, _ := m.store.ListUsers(); len(users) != 2 {
		t.Fatalf("%d users, want the referrer and one sign-up", len(users))
	}
}

func TestSignupRefusedWhileRegistrationIsClosed(t *testing.T) {
	t.Parallel()
	m, _ := signupFixture(t, model.RegOff, "")
	_, err := m.Signup(context.Background(), SignupRequest{ExternalID: "bob"})
	if errCode(err) != "err.signupClosed" {
		t.Fatalf("err = %v, want err.signupClosed", err)
	}
	if users, _ := m.store.ListUsers(); len(users) != 0 {
		t.Fatalf("%d users made while closed", len(users))
	}
}

func TestSignupNeedsTheInviteCode(t *testing.T) {
	t.Parallel()
	m, _ := signupFixture(t, model.RegInvite, "SECRET")
	ctx := context.Background()
	if _, err := m.Signup(ctx, SignupRequest{ExternalID: "a", Invite: "guess"}); errCode(err) != "err.signupBadInvite" {
		t.Fatalf("wrong code: err = %v", err)
	}
	// The wrong guess spent the id's slot: a second guess inside the minute is refused
	// before the code is even compared.
	if _, err := m.Signup(ctx, SignupRequest{ExternalID: "a", Invite: "SECRET"}); !errors.Is(err, ErrSignupBusy) {
		t.Fatalf("second try inside the minute: err = %v, want busy", err)
	}
	res, err := m.Signup(ctx, SignupRequest{ExternalID: "b", Invite: " SECRET "})
	if err != nil || res.Status != SignupCreated {
		t.Fatalf("right code: %+v, %v", res, err)
	}
}

// One address, or one IPv6 /64, signs up once a minute; another is not held up.
func TestSignupRateLimitsAnAddress(t *testing.T) {
	t.Parallel()
	m, _ := signupFixture(t, model.RegOpen, "")
	ctx := context.Background()
	steps := []struct {
		ext, ip string
		busy    bool
	}{
		{"c1", "203.0.113.7", false},
		{"c2", "203.0.113.7", true},
		{"c3", "203.0.113.8", false},
		{"c4", "2001:db8:1:2::1", false},
		{"c5", "2001:db8:1:2:ffff::9", true}, // the same /64
		{"c6", "2001:db8:1:3::1", false},
		{"c7", "", false},
	}
	for _, s := range steps {
		_, err := m.Signup(ctx, SignupRequest{ExternalID: s.ext, IP: s.ip})
		if busy := errors.Is(err, ErrSignupBusy); busy != s.busy || (err != nil && !busy) {
			t.Errorf("%s from %q: err = %v, want busy=%v", s.ext, s.ip, err, s.busy)
		}
	}
	if _, err := m.Signup(ctx, SignupRequest{ExternalID: "c8", IP: "not-an-ip"}); errCode(err) != "err.signupIPInvalid" {
		t.Errorf("bad ip: err = %v", err)
	}
}

func TestSignupValidatesTheExternalID(t *testing.T) {
	t.Parallel()
	m, _ := signupFixture(t, model.RegOpen, "")
	long := strings.Repeat("x", 255)
	for _, c := range []struct{ ext, code string }{
		{"  ", "err.externalIDRequired"},
		{"a\nb", "err.externalIDInvalid"},
		{long, "err.externalIDTooLong"},
	} {
		if _, err := m.Signup(context.Background(), SignupRequest{ExternalID: c.ext}); errCode(err) != c.code {
			t.Errorf("%.20q: err = %v, want %s", c.ext, err, c.code)
		}
	}
}

// Under moderation a sign-up is a request the operator decides, as a bot one is:
// asking again while it waits files nothing, approval makes the account with what the
// request came with, and rejection leaves none.
func TestSignupUnderModeration(t *testing.T) {
	t.Parallel()
	m, trial := signupFixture(t, model.RegModeration, "")
	ctx := context.Background()
	var prompted []int64
	m.SetAdminModerationNotifier(func(reqID int64, _, _ string) { prompted = append(prompted, reqID) })

	res, err := m.Signup(ctx, SignupRequest{ExternalID: "dee@example.com", Name: "Dee", Source: "site", IP: "203.0.113.20"})
	if err != nil || res.Status != SignupPending || res.RequestID == 0 {
		t.Fatalf("signup = %+v, %v", res, err)
	}
	if len(prompted) != 1 || prompted[0] != res.RequestID {
		t.Fatalf("admin prompted for %v, want [%d]", prompted, res.RequestID)
	}
	// Asked again at once — from the same address, inside the rate limit's minute:
	// the same request, not a refusal and not a second one.
	again, err := m.Signup(ctx, SignupRequest{ExternalID: "dee@example.com", IP: "203.0.113.20"})
	if err != nil || again.Status != SignupPending || again.RequestID != res.RequestID {
		t.Fatalf("asked again = %+v, %v", again, err)
	}
	reqs, _ := m.ListRegistrationRequests()
	if len(reqs) != 1 || reqs[0].ExternalID != "dee@example.com" || reqs[0].ChatID != 0 {
		t.Fatalf("queue = %+v", reqs)
	}
	if users, _ := m.store.ListUsers(); len(users) != 0 {
		t.Fatalf("an account exists before approval: %d", len(users))
	}

	if err := m.ApproveRegistrationRequest(ctx, res.RequestID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	after, err := m.Signup(ctx, SignupRequest{ExternalID: "dee@example.com"})
	if err != nil || after.Status != SignupExisting || after.User == nil {
		t.Fatalf("after approval = %+v, %v", after, err)
	}
	u := after.User
	if u.Name != "Dee" || u.PlanID != trial.ID || !u.TrialUsed || u.TgChatID != 0 {
		t.Fatalf("approved account = %+v", u)
	}
	if got := m.store.UserSource(u.ID); got != "site" {
		t.Errorf("source = %q", got)
	}
	// A second approval of the same (gone) request makes nothing.
	_ = m.ApproveRegistrationRequest(ctx, res.RequestID)

	rej, err := m.Signup(ctx, SignupRequest{ExternalID: "eve@example.com"})
	if err != nil || rej.Status != SignupPending {
		t.Fatalf("second applicant = %+v, %v", rej, err)
	}
	if err := m.RejectRegistrationRequest(ctx, rej.RequestID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if reqs, _ := m.ListRegistrationRequests(); len(reqs) != 0 {
		t.Fatalf("queue after the decisions = %+v", reqs)
	}
	if users, _ := m.store.ListUsers(); len(users) != 1 {
		t.Fatalf("%d users, want only the approved one", len(users))
	}
}

// A site that sends the same sign-up twice at once — a double-click, a retry — gets
// one account.
func TestSignupConcurrentSameExternalID(t *testing.T) {
	t.Parallel()
	m, _ := signupFixture(t, model.RegOpen, "")
	var wg sync.WaitGroup
	ids := make([]int64, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := m.Signup(context.Background(), SignupRequest{ExternalID: "same"})
			if err == nil && res.User != nil {
				ids[i] = res.User.ID
			}
		}()
	}
	wg.Wait()
	if users, _ := m.store.ListUsers(); len(users) != 1 {
		t.Fatalf("%d accounts for one external id", len(users))
	}
	for i, id := range ids {
		if id != ids[0] || id == 0 {
			t.Fatalf("call %d got user %d, call 0 got %d", i, id, ids[0])
		}
	}
}
