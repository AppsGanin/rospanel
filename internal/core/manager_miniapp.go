package core

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
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

// signupLimiter bounds self-registrations that no one else rate-limits: the Mini
// App's initData is only signed by Telegram, and a website's sign-up call comes from
// the operator's own backend on behalf of anyone who reaches its form.
type signupLimiter struct {
	mu  sync.Mutex
	at  []time.Time
	key map[string]time.Time
}

const signupsPerMinute = 20

// allow spends a slot: one a minute for each key (a chat, a website id, an address),
// and signupsPerMinute in all — so one caller retrying cannot use up everyone's. A
// key already busy refuses the attempt without spending anything.
func (l *signupLimiter) allow(now time.Time, keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-time.Minute)
	if l.key == nil {
		l.key = map[string]time.Time{}
	}
	for k, t := range l.key {
		if !t.After(cut) {
			delete(l.key, k)
		}
	}
	for _, k := range keys {
		if _, busy := l.key[k]; busy {
			return false
		}
	}
	kept := l.at[:0]
	for _, t := range l.at {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	l.at = kept
	if len(l.at) >= signupsPerMinute {
		return false
	}
	l.at = append(l.at, now)
	for _, k := range keys {
		l.key[k] = now
	}
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
	u, err := m.store.GetUserByTelegramChatID(chat)
	if err == nil {
		return MiniAppResult{UserID: u.ID}, nil
	}
	// Only a real "no such account" may lead to registering one: a failed read taken
	// for it would mint a trial and move the chat off the account it belongs to.
	if !errors.Is(err, sql.ErrNoRows) {
		return MiniAppResult{}, err
	}
	// One registration at a time, checked again inside: two opens of the app at once
	// must not make two accounts.
	m.miniRegMu.Lock()
	defer m.miniRegMu.Unlock()
	if u, err := m.store.GetUserByTelegramChatID(chat); err == nil {
		return MiniAppResult{UserID: u.ID}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return MiniAppResult{}, err
	}
	now := time.Now()
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
	u, err = m.store.GetDetachedUserByPrevChat(chat)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return MiniAppResult{}, err
	}
	if err == nil && u != nil {
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
	case set.RegMode() == model.RegModeration && m.RegistrationPending(chat):
		return MiniAppResult{Reason: "sub.miniRequested"}, nil
	case !m.miniSignups.allow(now, "tg:"+strconv.FormatInt(chat, 10)):
		return MiniAppResult{Reason: "sub.miniBusy"}, nil
	}
	_ = m.store.UpsertSubscriber(chat, 0, tu.Username, tu.FirstName, tu.Lang, now.Unix())
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
	u, err = m.CreateRegisteredUser(ctx, name)
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

// EnsureMiniAppPath gives the install its random Mini App address segment, once.
func (m *Manager) EnsureMiniAppPath() error {
	return m.store.EnsureMiniAppPath(func() string {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b)
	})
}
