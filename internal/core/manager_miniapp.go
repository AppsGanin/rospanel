package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The Mini App entrance: one fixed address (sub.MiniAppURL) the bot's menu button
// and t.me/<bot>?startapp=… links open. Telegram hands the page its initData, signed
// with the bot's token; the panel checks the signature, finds the account linked to
// that Telegram user and sends them to their own subscription page — no token in
// the link. Someone with no account is registered on the spot when sign-up is open
// (a request is filed under moderation), carrying the start parameter: ref_<code> is
// an invitation, anything else a source tag.

// miniAppMaxAge is how old initData may be. Telegram signs it when the app opens; a
// page kept open longer reopens it.
const miniAppMaxAge = 24 * time.Hour

// MiniAppUser is the Telegram user initData vouches for.
type MiniAppUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
	Lang      string `json:"language_code"`
}

// ErrMiniAppAuth is initData that does not check out.
var ErrMiniAppAuth = errors.New("mini app: initData does not check out")

// VerifyInitData checks initData against the bot token (Telegram's "Validating data
// received via the Mini App") and returns the user and the start parameter.
func VerifyInitData(initData, botToken string, now time.Time) (MiniAppUser, string, error) {
	vals, err := url.ParseQuery(initData)
	if err != nil || botToken == "" {
		return MiniAppUser{}, "", ErrMiniAppAuth
	}
	got := vals.Get("hash")
	if got == "" {
		return MiniAppUser{}, "", ErrMiniAppAuth
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+vals.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(got))) {
		return MiniAppUser{}, "", ErrMiniAppAuth
	}
	at, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil || now.Sub(time.Unix(at, 0)) > miniAppMaxAge || time.Unix(at, 0).After(now.Add(5*time.Minute)) {
		return MiniAppUser{}, "", ErrMiniAppAuth
	}
	var u MiniAppUser
	if err := json.Unmarshal([]byte(vals.Get("user")), &u); err != nil || u.ID <= 0 {
		return MiniAppUser{}, "", ErrMiniAppAuth
	}
	return u, vals.Get("start_param"), nil
}

// MiniAppResult says where the Mini App goes: the user's page, or why not (Reason
// is a dictionary key the page words, with the bot link to go on from there).
type MiniAppResult struct {
	UserID int64
	Reason string // "" = UserID is set; else sub.miniRegClosed | sub.miniRequested | sub.miniRefused | sub.miniBusy
}

// miniAppSignups bounds registrations through the Mini App: its initData is only
// signed by Telegram, not rate-limited by it.
type miniAppSignups struct {
	mu sync.Mutex
	at []time.Time
}

const miniAppSignupsPerMinute = 20

func (l *miniAppSignups) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-time.Minute)
	kept := l.at[:0]
	for _, t := range l.at {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	l.at = kept
	if len(l.at) >= miniAppSignupsPerMinute {
		return false
	}
	l.at = append(l.at, now)
	return true
}

// MiniAppEnter takes a Telegram user in: their account, the one they unlinked, a
// new one when sign-up is open, or the reason there is none.
func (m *Manager) MiniAppEnter(ctx context.Context, tu MiniAppUser, startParam string) (MiniAppResult, error) {
	set, err := m.Settings()
	if err != nil {
		return MiniAppResult{}, err
	}
	chat := tu.ID // a private chat's id is the user's
	if u, err := m.store.GetUserByTelegramChatID(chat); err == nil {
		return MiniAppResult{UserID: u.ID}, nil
	}
	now := time.Now()
	_ = m.store.UpsertSubscriber(chat, 0, tu.Username, tu.FirstName, tu.Lang, now.Unix())
	if p := strings.TrimSpace(startParam); p != "" {
		if code, ok := strings.CutPrefix(p, "ref_"); ok {
			if code != "" {
				m.TrackReferral(chat, code)
			}
		} else {
			m.TrackSource(chat, p)
		}
	}
	// Unlinked before: the same account back, not a fresh trial.
	if u, err := m.store.GetDetachedUserByPrevChat(chat); err == nil && u != nil {
		if err := m.store.SetUserTelegramChat(u.ID, chat); err != nil {
			return MiniAppResult{}, err
		}
		m.AuditTelegramLinked(ctx, u.ID, tu.Username)
		return MiniAppResult{UserID: u.ID}, nil
	}
	switch {
	case !set.RegistrationOpen():
		return MiniAppResult{Reason: "sub.miniRegClosed"}, nil
	case m.RegistrationBlacklisted(chat):
		return MiniAppResult{Reason: "sub.miniRefused"}, nil
	case set.RegMode() == model.RegInvite:
		// The invite code is asked for in the bot.
		return MiniAppResult{Reason: "sub.miniRegClosed"}, nil
	case !m.miniSignups.allow(now):
		return MiniAppResult{Reason: "sub.miniBusy"}, nil
	}
	name := strings.TrimSpace(tu.FirstName)
	if name == "" {
		name = strconv.FormatInt(tu.ID, 10)
	}
	if set.RegMode() == model.RegModeration {
		if _, err := m.RequestRegistration(ctx, chat, name); err != nil {
			return MiniAppResult{}, err
		}
		return MiniAppResult{Reason: "sub.miniRequested"}, nil
	}
	u, err := m.CreateRegisteredUser(ctx, name)
	if err != nil {
		return MiniAppResult{}, err
	}
	if err := m.store.SetUserTelegramChat(u.ID, chat); err != nil {
		return MiniAppResult{}, err
	}
	m.AuditTelegramLinked(ctx, u.ID, tu.Username)
	m.AttachReferrer(ctx, u.ID, chat)
	return MiniAppResult{UserID: u.ID}, nil
}
