package core

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The churn survey: a user whose paid term lapsed is asked why they did not renew —
// by the user bot a day later, and on the subscription page while it stays lapsed.
// One answer per lapse; the operator reads them beside the funnel.

const (
	// churnAskAfter is how long after a lapse the bot asks: long enough for a renewal
	// that was only late, short enough to be remembered.
	churnAskAfter = 24 * time.Hour
	// churnCatchUp is how far past that a lapse is still asked about, so switching
	// the survey on does not write to everyone who ever left.
	churnCatchUp = 7 * 24 * time.Hour
	// churnCommentsShown is how many answers with words the funnel lists.
	churnCommentsShown = 30
)

// SetUserSurveyor registers the user bot's sender of the survey question. It sends
// before returning.
func (m *Manager) SetUserSurveyor(fn func(chatID int64)) {
	m.notifyMu.Lock()
	m.userSurvey = fn
	m.notifyMu.Unlock()
}

// churnSurveyOn reports whether the survey is asked at all.
func churnSurveyOn(set *model.Settings) bool {
	return set.BillingEnabled && set.ChurnSurvey
}

// runChurnSurvey asks the users whose lapse is due, at most once an hour.
func (m *Manager) runChurnSurvey(set *model.Settings, now int64) {
	if !churnSurveyOn(set) || !set.TGUserBotEnabled {
		return
	}
	last := m.surveyAt.Load()
	if now-last < int64(winbackEvery.Seconds()) || !m.surveyAt.CompareAndSwap(last, now) {
		return
	}
	m.notifyMu.Lock()
	ask := m.userSurvey
	m.notifyMu.Unlock()
	if ask == nil {
		return
	}
	cut := now - int64(churnAskAfter.Seconds())
	cands, err := m.store.SurveyCandidates(cut-int64(churnCatchUp.Seconds()), cut, winbackBatch)
	if err != nil {
		logErr("churn survey: candidates", "err", err)
		return
	}
	var chats []int64
	for _, c := range cands {
		u, err := m.store.GetUser(c.UserID)
		if err != nil {
			continue
		}
		// Marked before asking, and whether or not it is asked: the lapse is handled
		// either way, and a failed send must not ask again every hour.
		if ok, err := m.store.MarkSurveyed(u.ID, c.Lapse); err != nil || !ok {
			continue
		}
		if u.TgChatID == 0 || m.ActivePaidPlan(*u) != nil {
			continue
		}
		if done, err := m.store.ChurnAnswered(u.ID, c.Lapse); err != nil || done {
			continue
		}
		chats = append(chats, u.TgChatID)
	}
	// Sent at the bot's pace off the billing sweep, which must not wait on Telegram.
	if len(chats) > 0 {
		go func() {
			for _, chat := range chats {
				ask(chat)
			}
		}()
	}
}

// ChurnSurveyOpen reports whether the user can answer the survey now: it is on, their
// paid term lapsed with nothing paid since, and they have not answered for it.
func (m *Manager) ChurnSurveyOpen(u model.User) bool {
	set, err := m.Settings()
	if err != nil || !churnSurveyOn(set) {
		return false
	}
	lapse, ok := m.churnLapse(u)
	if !ok {
		return false
	}
	done, err := m.store.ChurnAnswered(u.ID, lapse)
	return err == nil && !done
}

// churnLapse is the lapse the user can answer for, false when there is none.
func (m *Manager) churnLapse(u model.User) (int64, bool) {
	if m.ActivePaidPlan(u) != nil {
		return 0, false
	}
	lapse, err := m.store.SurveyLapse(u.ID, time.Now().Unix())
	if err != nil || lapse == 0 {
		return 0, false
	}
	return lapse, true
}

// AnswerChurn records why the user did not renew. source is "bot" or "page". An
// answer can be given again (the reason, then the words after "other"); the first
// one is what reaches the journal and the webhook.
func (m *Manager) AnswerChurn(ctx context.Context, userID int64, reason, comment, source string) error {
	if !model.ValidChurnReason(reason) {
		return invalidCode("err.churnReason", "неизвестная причина")
	}
	comment = strings.TrimSpace(comment)
	if utf8.RuneCountInString(comment) > model.ChurnCommentMax {
		comment = string([]rune(comment)[:model.ChurnCommentMax])
	}
	set, err := m.Settings()
	if err != nil {
		return err
	}
	if !churnSurveyOn(set) {
		return invalidCode("err.churnClosed", "опрос сейчас не проводится")
	}
	u, err := m.store.GetUser(userID)
	if err != nil {
		return invalidCode("err.userNotFound", "пользователь не найден")
	}
	lapse, ok := m.churnLapse(*u)
	if !ok {
		return invalidCode("err.churnClosed", "опрос сейчас не проводится")
	}
	fresh, worded, err := m.store.SaveChurnAnswer(model.ChurnAnswer{
		UserID: u.ID, Reason: reason, Comment: comment, Source: source, CreatedAt: time.Now().Unix(),
	}, lapse)
	if err != nil {
		return err
	}
	// Answered is asked: the bot does not ask after an answer on the page, and the
	// next survey waits for a payment.
	if _, err := m.store.MarkSurveyed(u.ID, lapse); err != nil {
		logErr("churn survey: marking the lapse failed", "user", u.ID, "err", err)
	}
	details := map[string]any{"reason": reason, "source": source}
	if comment != "" {
		details["comment"] = comment
	}
	// Only news reaches the journal and the webhook: the first answer, and the first
	// words — pressing the buttons again must not flood either.
	if fresh || worded {
		m.auditNamed(ctx, u.ID, u.Name, model.EventChurnAnswer, details)
		m.emitUserWebhook(model.WebhookUserChurnAnswer, u.ID, details)
	}
	return nil
}

// ChurnStats sums up the answers of the last days (0 = all time).
func (m *Manager) ChurnStats(days int) (model.ChurnStats, error) {
	var since int64
	if days > 0 {
		since = time.Now().AddDate(0, 0, -days).Unix()
	}
	return m.store.ChurnStats(since, churnCommentsShown)
}
