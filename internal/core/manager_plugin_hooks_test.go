package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// fakeHooks answers the panel's decisions the way a plugin would.
type fakeHooks struct {
	refuse    string // a username or external id refused at sign-up
	refuseAll bool
	price     int
	signups   atomic.Int32
}

func (f *fakeHooks) Hooked(string) bool { return true }

func (f *fakeHooks) BeforeSignup(_ context.Context, req model.SignupCheck) (bool, string) {
	f.signups.Add(1)
	if f.refuseAll || req.ExternalID == f.refuse || req.Username == f.refuse {
		return false, "not today"
	}
	return true, ""
}

func (f *fakeHooks) BeforeDeviceBind(_ context.Context, req model.DeviceCheck) (bool, string) {
	return req.DeviceOS != "evil", ""
}

func (f *fakeHooks) QuotePrice(_ context.Context, req model.PriceRequest) (int, string, bool) {
	return f.price, "regional", f.price != 0
}

// A plugin's refusal stops a website sign-up before any account is made, with its
// reason in the error; an account that exists is never asked about.
func TestSignupRefusedByPlugin(t *testing.T) {
	t.Parallel()
	m, _ := signupFixture(t, model.RegOpen, "")
	hooks := &fakeHooks{refuse: "bob@x.io"}
	m.SetPluginHooks(hooks)
	ctx := context.Background()
	_, err := m.Signup(ctx, SignupRequest{ExternalID: "bob@x.io"})
	var ve *ValidationError
	if errCode(err) != "err.pluginDenied" || !errors.As(err, &ve) || ve.Args["detail"] != "not today" {
		t.Fatalf("err = %v", err)
	}
	if users, _ := m.store.ListUsers(); len(users) != 0 {
		t.Fatalf("%d users made though refused", len(users))
	}
	if _, err := m.Signup(ctx, SignupRequest{ExternalID: "ann@x.io"}); err != nil {
		t.Fatal(err)
	}
	asked := hooks.signups.Load()
	if res, err := m.Signup(ctx, SignupRequest{ExternalID: "ann@x.io"}); err != nil || res.Status != SignupExisting {
		t.Fatalf("%+v %v", res, err)
	}
	if hooks.signups.Load() != asked {
		t.Fatal("a returning account was put to the plugins")
	}
}

// A plugin's price goes in before the promo code and only within half to all of
// the base; outside it the panel's own price stands.
func TestQuotePlanTakesAPluginPrice(t *testing.T) {
	t.Parallel()
	m, st, plan := walletFixture(t)
	u := walletUser(t, st, "priced")
	hooks := &fakeHooks{price: 150}
	m.SetPluginHooks(hooks)
	q := m.QuotePlan(*u, plan)
	if q.TotalRub != 150 || q.MoneyRub != 150 || q.PluginDiscountRub != 49 || q.PluginNote != "regional" {
		t.Fatalf("plugin price: %+v", q)
	}
	for _, p := range []int{99, 199, 500} { // under half, the base itself, over it
		hooks.price = p
		if q := m.QuotePlan(*u, plan); q.TotalRub != 199 || q.PluginDiscountRub != 0 {
			t.Fatalf("price %d: %+v", p, q)
		}
	}
}

// A plugin is asked about a device the user has not bound yet, never about one
// refreshing; its refusal binds nothing.
func TestAdmitDeviceAsksPlugins(t *testing.T) {
	t.Parallel()
	m, st, _ := walletFixture(t)
	u := walletUser(t, st, "devices")
	m.SetPluginHooks(&fakeHooks{})
	set := &model.Settings{HWIDEnabled: true}
	ctx := context.Background()
	if v := m.AdmitDevice(ctx, *u, set, model.Device{HWID: "a", OS: "evil"}); v.Allow || !v.ByPlugin {
		t.Fatalf("evil: %+v", v)
	}
	if n, _ := st.CountDevices(u.ID); n != 0 {
		t.Fatalf("a refused device was bound (%d)", n)
	}
	if v := m.AdmitDevice(ctx, *u, set, model.Device{HWID: "b", OS: "ios"}); !v.Allow {
		t.Fatalf("ios: %+v", v)
	}
	// Bound now: the same id is a refresh, whatever it says about itself.
	if v := m.AdmitDevice(ctx, *u, set, model.Device{HWID: "b", OS: "evil"}); !v.Allow {
		t.Fatalf("a bound device was put to the plugins: %+v", v)
	}
}

// A Telegram that unlinked its account gets that account back without the plugins
// being asked: it is not a sign-up, and a plugin must not be able to lock someone
// out of their own account.
func TestTelegramRestoreIsNotPutToPlugins(t *testing.T) {
	t.Parallel()
	m, _ := signupFixture(t, model.RegOpen, "")
	ctx := context.Background()
	u, err := m.store.CreateUser("back", "uuid-back", "pw", "tok-back", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetUserTelegramChat(u.ID, 4242); err != nil {
		t.Fatal(err)
	}
	if err := m.store.ClearUserTelegramChat(u.ID); err != nil {
		t.Fatal(err)
	}
	hooks := &fakeHooks{refuseAll: true}
	m.SetPluginHooks(hooks)
	res, err := m.Signup(ctx, SignupRequest{TelegramID: 4242})
	if err != nil || res.Status != SignupExisting || res.User.ID != u.ID {
		t.Fatalf("%+v %v", res, err)
	}
	if hooks.signups.Load() != 0 {
		t.Fatal("a restore was put to the plugins")
	}
	if _, err := m.Signup(ctx, SignupRequest{TelegramID: 4343}); errCode(err) != "err.pluginDenied" {
		t.Fatalf("a new Telegram: %v", err)
	}
}
