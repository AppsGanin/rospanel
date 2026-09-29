package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
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

// sourceMax bounds a source tag: Telegram's own /start payload limit.
const sourceMax = 64

// NormalizeSource makes a source tag of free text: lower case, letters, digits, "_"
// and "-" only, at most 64 characters. "" when nothing is left.
func NormalizeSource(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if b.Len() >= sourceMax {
			break
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TrackSource remembers the tag a chat with no account yet arrived with. The first
// tag a chat brings is the one kept.
func (m *Manager) TrackSource(chatID int64, tag string) {
	if tag = NormalizeSource(tag); tag == "" {
		return
	}
	if err := m.store.SetSubscriberSource(chatID, tag, time.Now().Unix()); err != nil {
		logErr("source: remember tag failed", "chat", chatID, "err", err)
	}
}

// SetUserSource sets where a user came from — what an outside bot does with the tag
// its /start carried. "" clears it.
func (m *Manager) SetUserSource(ctx context.Context, userID int64, tag string) error {
	tag = NormalizeSource(tag)
	if err := m.store.SetUserSource(userID, tag); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return invalidCode("err.userNotFound", "пользователь не найден")
		}
		return err
	}
	m.audit(ctx, userID, model.EventUserSource, map[string]any{"source": tag})
	return nil
}

// UserSource reads where a user came from.
func (m *Manager) UserSource(userID int64) string { return m.store.UserSource(userID) }

// FunnelBySource is Funnel split by where the users came from.
func (m *Manager) FunnelBySource(days int) ([]model.SourceFunnel, error) {
	var since int64
	if days > 0 {
		since = time.Now().AddDate(0, 0, -days).Unix()
	}
	return m.store.FunnelBySource(since, 30)
}

// FraudSignals lists the patterns worth an operator's look (see store.FraudSignals).
func (m *Manager) FraudSignals() ([]model.FraudSignal, error) {
	now := time.Now()
	sigs, err := m.store.FraudSignals(store.FraudWindow{
		Since:     now.AddDate(0, 0, -model.FraudWindowDays).Unix(),
		WeekSince: now.AddDate(0, 0, -7).Unix(),
	})
	if sigs == nil {
		sigs = []model.FraudSignal{}
	}
	return sigs, err
}
