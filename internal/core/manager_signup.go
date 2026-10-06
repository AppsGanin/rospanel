package core

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Sign-up from the operator's own website: the same self-registration the user bot
// runs, for a client the site knows by its own id instead of a Telegram chat. The
// site's backend calls it (POST /v1/signup) for whoever filled in its form, so it
// keeps every rule the bot keeps — registration open or closed, the invite code,
// moderation, the rate limit, one trial per client — rather than leaving them to the
// site. Creating a user through POST /v1/users is the operator's own act and keeps
// none of them.

// SignupRequest is one website sign-up.
type SignupRequest struct {
	// ExternalID is the external system's id for its client: an e-mail, an account
	// number. Compared exactly, so it is normalised first (an e-mail in lower case).
	// TelegramID instead signs up a Telegram user, under the panel's rules for a
	// Telegram (see signupTelegram). One of the two.
	ExternalID string
	TelegramID int64
	Name       string // the panel's name for the account; the external id when empty
	Source     string // where the client came from, as a /start tag is
	Ref        string // an invite code ("r_<code>" or bare); an unknown one is ignored
	Invite     string // the registration code, when sign-up is by invitation
	IP         string // the client's address, for the rate limit; optional
}

// Signup outcomes.
const (
	SignupCreated  = "created"  // a new account
	SignupExisting = "existing" // this external id already has one
	SignupPending  = "pending"  // filed for an operator's decision (moderation)
)

// SignupResult says what a sign-up came to: the account, or the pending request.
type SignupResult struct {
	Status    string
	User      *model.User
	RequestID int64
}

// ErrSignupBusy is a sign-up refused by the rate limit; try again in a minute.
var ErrSignupBusy = errors.New("signup: too many sign-ups, try again in a minute")

// maxExternalIDLen bounds a website id: room for any e-mail address.
const maxExternalIDLen = 254

func cleanExternalID(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", invalidCode("err.externalIDRequired", "укажите external_id — id клиента во внешней системе — или telegram_id")
	}
	if len([]rune(s)) > maxExternalIDLen {
		return "", invalidCode("err.externalIDTooLong", "external_id длиннее {{max}} символов", map[string]any{"max": maxExternalIDLen})
	}
	if strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", invalidCode("err.externalIDInvalid", "external_id содержит управляющие символы")
	}
	return s, nil
}

// SetUserExternalID gives an existing account its website id, or with "" takes it
// away — how a site ties its own login to a client who came from the bot or the Mini
// App, instead of a sign-up making them a second account. An id another account holds
// is refused: whoever holds the id is that account on the site.
func (m *Manager) SetUserExternalID(ctx context.Context, userID int64, raw string) error {
	ext := strings.TrimSpace(raw)
	if ext != "" {
		var err error
		if ext, err = cleanExternalID(ext); err != nil {
			return err
		}
	}
	u, err := m.store.GetUser(userID)
	if err != nil {
		return err
	}
	if m.store.UserExternalID(userID) == ext {
		return nil
	}
	if err := m.store.SetUserExternalID(userID, ext); err != nil {
		if errors.Is(err, store.ErrExternalIDTaken) {
			return invalidCode("err.externalIDTaken", "этот external_id уже у другого пользователя")
		}
		return err
	}
	m.auditNamed(ctx, u.ID, u.Name, model.EventUserExternalID, map[string]any{"external_id": ext})
	return nil
}

// signupAddrKey is the rate-limit key of a client address. An IPv6 client is counted
// by its /64: one host is handed a whole one and could step through it.
func signupAddrKey(s string) (string, error) {
	if s = strings.TrimSpace(s); s == "" {
		return "", nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", invalidCode("err.signupIPInvalid", "ip должен быть адресом IPv4 или IPv6")
	}
	a = a.Unmap()
	if a.Is6() {
		p, _ := a.Prefix(64)
		return "ip:" + p.String(), nil
	}
	return "ip:" + a.String(), nil
}

// Signup registers a website client, or finds the account they already have.
func (m *Manager) Signup(ctx context.Context, req SignupRequest) (SignupResult, error) {
	addrKey, err := signupAddrKey(req.IP)
	if err != nil {
		return SignupResult{}, err
	}
	if req.TelegramID != 0 {
		if strings.TrimSpace(req.ExternalID) != "" {
			return SignupResult{}, invalidCode("err.signupOneID", "укажите external_id или telegram_id, не оба")
		}
		return m.signupTelegram(ctx, req, addrKey)
	}
	ext, err := cleanExternalID(req.ExternalID)
	if err != nil {
		return SignupResult{}, err
	}
	// A client who has an account gets it back whatever the settings are now: that
	// is the site signing them in, not a new sign-up.
	if res, ok, err := m.signupExisting(ext); ok || err != nil {
		return res, err
	}
	m.signupMu.Lock()
	defer m.signupMu.Unlock()
	if res, ok, err := m.signupExisting(ext); ok || err != nil {
		return res, err
	}
	set, err := m.Settings()
	if err != nil {
		return SignupResult{}, err
	}
	if !set.RegistrationOpen() {
		return SignupResult{}, invalidCode("err.signupClosed", "регистрация закрыта")
	}
	moderation := set.RegMode() == model.RegModeration
	if moderation {
		// Asking again while the request waits costs nothing and files nothing.
		r, err := m.store.GetRegistrationRequestByExternal(ext)
		if err != nil {
			return SignupResult{}, err
		}
		if r != nil {
			return SignupResult{Status: SignupPending, RequestID: r.ID}, nil
		}
	}
	// Spent before the invite code is checked, as the bot does: guessing the code
	// must cost something.
	keys := []string{"ext:" + ext}
	if addrKey != "" {
		keys = append(keys, addrKey)
	}
	if !m.webSignups.allow(time.Now(), keys...) {
		return SignupResult{}, ErrSignupBusy
	}
	if set.RegMode() == model.RegInvite {
		want := strings.TrimSpace(set.TGUserRegCode)
		if want == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(req.Invite)), []byte(want)) != 1 {
			return SignupResult{}, invalidCode("err.signupBadInvite", "неверный код-приглашение")
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = ext
	}
	name = truncateName(name)
	source := NormalizeSource(req.Source)
	refID := m.signupReferrer(set, req.Ref)
	if moderation {
		r, err := m.store.CreateWebRegistrationRequest(ext, name, source, refID, time.Now().Unix())
		filed := err == nil
		if errors.Is(err, store.ErrRegistrationPending) {
			r, err = m.store.GetRegistrationRequestByExternal(ext)
		}
		if err != nil {
			return SignupResult{}, err
		}
		if r == nil {
			return SignupResult{}, errors.New("signup: the request vanished as it was filed")
		}
		m.notifyModeration(*r)
		if filed { // asking again while it waits files nothing new
			m.EmitWebhook(model.WebhookRegistrationRequested, map[string]any{
				"request_id": r.ID, "name": r.Name, "external_id": ext,
			})
		}
		return SignupResult{Status: SignupPending, RequestID: r.ID}, nil
	}
	u, err := m.createWebUser(ctx, ext, name, source, refID)
	if err != nil {
		return SignupResult{}, err
	}
	m.announceRegistration(ctx, u, ext, nil)
	return SignupResult{Status: SignupCreated, User: u}, nil
}

// signupExisting answers a sign-up whose external id already has an account.
func (m *Manager) signupExisting(ext string) (SignupResult, bool, error) {
	id, err := m.store.UserIDByExternalID(ext)
	if err != nil || id == 0 {
		return SignupResult{}, false, err
	}
	u, err := m.store.GetUser(id)
	if err != nil {
		return SignupResult{}, false, err
	}
	return SignupResult{Status: SignupExisting, User: u}, true, nil
}

// signupReferrer resolves an invite code to the user who shared it; 0 when the code
// names nobody or the programme is off. A bad code does not cost the client their
// sign-up — the bot ignores one the same way.
func (m *Manager) signupReferrer(set *model.Settings, code string) int64 {
	code = strings.TrimPrefix(strings.TrimSpace(code), "r_")
	if code == "" || !set.RefEnabled() {
		return 0
	}
	return m.store.UserIDByRefCode(code)
}

// createWebUser makes a website client's account — trial, free plan or plain, as
// for any self-registration — and gives it the external id, source and referrer.
// Caller holds signupMu.
func (m *Manager) createWebUser(ctx context.Context, ext, name, source string, refID int64) (*model.User, error) {
	u, err := m.createRegisteredUser(name, true)
	if err != nil {
		return nil, err
	}
	if err := m.store.SetUserExternalID(u.ID, ext); err != nil {
		// An account the site cannot find again is worse than none: it would be
		// minted afresh, with a second trial, on the next attempt.
		_ = m.store.DeleteUser(u.ID)
		m.TriggerUserSync()
		return nil, err
	}
	if source != "" {
		if err := m.store.SetUserSource(u.ID, source); err != nil {
			logErr("signup: source not recorded", "user", u.ID, "err", err)
		}
	}
	if refID != 0 {
		ok, err := m.store.SetReferrer(u.ID, refID)
		if err != nil {
			logErr("signup: referrer not recorded", "user", u.ID, "err", err)
		} else if ok {
			m.referred(ctx, u.ID, refID)
		}
	}
	return u, nil
}

// approveWebRequest turns a website client's pending request into their account.
// Claimed under signupMu, so a sign-up asking at the same moment sees either the
// request or the account — never neither, which would file a second request.
func (m *Manager) approveWebRequest(ctx context.Context, req *model.RegistrationRequest) error {
	m.signupMu.Lock()
	defer m.signupMu.Unlock()
	claimed, err := m.store.ClaimRegistrationRequest(req.ID)
	if err != nil || !claimed {
		return err // another admin already decided it
	}
	if id, err := m.store.UserIDByExternalID(req.ExternalID); err != nil || id != 0 {
		return err // they already have an account
	}
	u, err := m.createWebUser(ctx, req.ExternalID, req.Name, req.Source, req.ReferrerID)
	if err != nil {
		// Back in the queue rather than lost: the client is still waiting.
		_ = m.store.RestoreRegistrationRequest(req)
		return err
	}
	m.announceRegistration(ctx, u, req.ExternalID, req)
	return nil
}
