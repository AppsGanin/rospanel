package core

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Win-back: a user whose paid term lapsed gets, some days later, a personal one-use
// discount code — attached to their next payment, and sent to them in the bot.

const (
	// winbackEvery is how often the sweep looks for lapsed users; lapses are counted
	// in days, so hourly is plenty.
	winbackEvery = time.Hour
	// winbackCatchUp is how far past the delay a lapse still earns a code. Switching
	// the feature on must not write to everyone who ever left.
	winbackCatchUp = 7 * 24 * time.Hour
	// winbackBatch bounds one sweep; the rest wait for the next hour.
	winbackBatch = 200
)

// codeAlphabet leaves out look-alikes: a code is read off a screen and retyped.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newWinbackCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return "BACK" + string(b)
}

// runWinback sends the codes that are due, at most once per winbackEvery.
func (m *Manager) runWinback(set *model.Settings, now int64) {
	if !set.BillingEnabled || !set.Winback.Enabled {
		return
	}
	last := m.winbackAt.Load()
	if now-last < int64(winbackEvery.Seconds()) || !m.winbackAt.CompareAndSwap(last, now) {
		return
	}
	cut := now - int64(set.Winback.AfterDays)*86400
	cands, err := m.store.WinbackCandidates(cut-int64(winbackCatchUp.Seconds()), cut, winbackBatch)
	if err != nil {
		logErr("winback: candidates", "err", err)
		return
	}
	for _, c := range cands {
		m.sendWinback(set, c, now)
	}
}

// sendWinback mints and sends one user's code — unless they came back meanwhile.
func (m *Manager) sendWinback(set *model.Settings, c store.WinbackCandidate, now int64) {
	u, err := m.store.GetUser(c.UserID)
	if err != nil {
		return
	}
	if m.ActivePaidPlan(*u) != nil {
		_ = m.store.SkipWinback(u.ID, c.Lapse)
		return
	}
	p := &model.PromoCode{
		Kind: model.PromoPercent, Value: set.Winback.Percent,
		ExpiresAt: now + int64(set.Winback.ValidDays)*86400,
		Note:      i18n.T(m.botLang(), "promo.winbackNote", u.Name),
	}
	// With no bot to tell the user, the code only helps attached to their next payment.
	canTell := u.TgChatID != 0 && set.TGUserBotEnabled
	attached, err := m.store.IssueWinback(u.ID, c.Lapse, p, newWinbackCode, !canTell, now)
	if errors.Is(err, store.ErrPromoUsed) {
		return
	}
	if errors.Is(err, store.ErrWinbackNowhere) {
		_ = m.store.SkipWinback(u.ID, c.Lapse)
		return
	}
	if err != nil {
		logErr("winback: code not issued", "user", u.ID, "err", err)
		return
	}
	m.auditNamed(context.Background(), u.ID, u.Name, model.EventWinbackSent, map[string]any{
		"code": p.Code, "percent": p.Value, "expires_at": p.ExpiresAt,
	})
	if !canTell {
		return
	}
	lang := m.userLang(u.TgChatID)
	until := time.Unix(p.ExpiresAt, 0).In(m.loc()).Format("02.01.2006")
	key := "notify.winback"
	if !attached {
		key = "notify.winbackEnter"
	}
	m.notifyUser(u.TgChatID, i18n.T(lang, key, p.Value, escHTML(p.Code), until))
}

// WinbackStats sums up the win-back codes sent so far.
func (m *Manager) WinbackStats() (model.WinbackStats, error) {
	return m.store.WinbackStats()
}

// Funnel follows the users who joined in the last days (0 = all time).
func (m *Manager) Funnel(days int) (model.Funnel, error) {
	var since int64
	if days > 0 {
		since = time.Now().AddDate(0, 0, -days).Unix()
	}
	return m.store.Funnel(since)
}
