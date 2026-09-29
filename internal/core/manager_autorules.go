package core

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Automatic messages: rules that write to people through the user bot when they
// reach a point in their life as a customer — opened the bot and never registered,
// registered and never connected, took the trial and never paid, stopped using a
// live subscription, let a paid term run out. Each person hears from a rule once per
// cycle, delay_hours after the trigger; with a discount a personal one-use code goes
// with the message, already attached to their next payment.

const (
	// autoRulesEvery is how often the rules look for people who became due.
	autoRulesEvery = 10 * time.Minute
	// autoRuleCatchUp is how far past its delay a trigger still gets the message:
	// a new rule must not write to everyone who ever matched it.
	autoRuleCatchUp = 7 * 24 * time.Hour
	// autoRuleBatch bounds one rule's sends per sweep; the rest wait for the next.
	autoRuleBatch = 200
	// autoRuleGap is the pause between two messages of a sweep: about 20 a second.
	autoRuleGap = 50 * time.Millisecond

	autoRuleNameMax     = 100
	autoRuleDelayMax    = 365 * 24
	autoRuleDiscountMax = 90
	autoRuleDaysMax     = 90
)

// SetUserMessenger registers the user bot's sender for messages with URL buttons. It
// sends before returning, and says whether the message went — a chat that blocked
// the bot counts as delivered (there is nothing to retry).
func (m *Manager) SetUserMessenger(fn func(chatID int64, html string, buttons []model.BroadcastButton) error) {
	m.notifyMu.Lock()
	m.userMessage = fn
	m.notifyMu.Unlock()
}

func (m *Manager) messenger() func(int64, string, []model.BroadcastButton) error {
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	return m.userMessage
}

// ListAutoRules returns the rules with what each did.
func (m *Manager) ListAutoRules() ([]model.AutoRule, error) {
	rules, err := m.store.ListAutoRules()
	if rules == nil {
		rules = []model.AutoRule{}
	}
	return rules, err
}

// SaveAutoRule checks and stores a rule.
func (m *Manager) SaveAutoRule(r *model.AutoRule) error {
	r.Name = strings.TrimSpace(r.Name)
	switch {
	case r.Name == "":
		return invalidCode("err.ruleNameRequired", "укажите название")
	case len([]rune(r.Name)) > autoRuleNameMax:
		return invalidCode("err.ruleNameTooLong", "название длиннее {{max}} символов", map[string]any{"max": autoRuleNameMax})
	case !model.ValidAutoRuleTrigger(r.Trigger):
		return invalidCode("err.ruleTrigger", "неизвестное событие")
	case r.DelayHours < 1 || r.DelayHours > autoRuleDelayMax:
		return invalidCode("err.ruleDelay", "задержка — от часа до года")
	case r.Trigger == model.TriggerIdle && r.DelayHours < model.AutoRuleIdleMinHours:
		return invalidCode("err.ruleIdleDelay", "для «не пользуется подпиской» задержка — от {{min}} часов", map[string]any{"min": model.AutoRuleIdleMinHours})
	case r.DiscountPercent < 0 || r.DiscountPercent > autoRuleDiscountMax:
		return invalidCode("err.ruleDiscount", "скидка — от 0 до {{max}}%", map[string]any{"max": autoRuleDiscountMax})
	case r.DiscountPercent > 0 && r.Trigger == model.TriggerNoSignup:
		return invalidCode("err.ruleDiscountNoAccount", "без аккаунта код не к чему привязать — уберите скидку")
	case r.DiscountPercent > 0 && (r.DiscountDays < 1 || r.DiscountDays > autoRuleDaysMax):
		return invalidCode("err.ruleDiscountDays", "срок кода — от 1 до {{max}} дней", map[string]any{"max": autoRuleDaysMax})
	}
	if r.DiscountPercent == 0 {
		r.DiscountDays = 0
	}
	b := model.Broadcast{Text: strings.TrimSpace(r.Text), Buttons: r.Buttons, Audience: model.AudienceAll}
	if err := validateBroadcast(&b); err != nil {
		return err
	}
	r.Text, r.Buttons = b.Text, b.Buttons
	if r.Buttons == nil {
		r.Buttons = []model.BroadcastButton{}
	}
	err := m.store.SaveAutoRule(r, time.Now().Unix())
	if errors.Is(err, sql.ErrNoRows) {
		return invalidCode("err.ruleNotFound", "правило не найдено")
	}
	return err
}

// DeleteAutoRule drops a rule; codes it gave out stay valid.
func (m *Manager) DeleteAutoRule(id int64) error { return m.store.DeleteAutoRule(id) }

// RunAutoRulesLoop sends what the rules have due.
func (m *Manager) RunAutoRulesLoop(ctx context.Context) {
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		m.RunAutoRules(time.Now().Unix())
		timer.Reset(autoRulesEvery)
	}
}

// RunAutoRules sends every enabled rule's due messages once.
func (m *Manager) RunAutoRules(now int64) {
	set, err := m.Settings()
	send := m.messenger()
	if err != nil || !set.TGUserBotEnabled || send == nil {
		return
	}
	rules, err := m.store.ListAutoRulesBare()
	if err != nil {
		logErr("auto messages: list", "err", err)
		return
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		// Asking someone to register makes sense only where they can, without an
		// operator deciding on them (a moderation refusal leaves no trace to skip).
		if r.Trigger == model.TriggerNoSignup && (!set.RegistrationOpen() || set.RegMode() == model.RegModeration) {
			continue
		}
		cut := now - int64(r.DelayHours)*3600
		targets, err := m.store.AutoRuleTargets(r, cut-int64(autoRuleCatchUp.Seconds()), cut, now, autoRuleBatch)
		if err != nil {
			logErr("auto messages: targets", "rule", r.ID, "err", err)
			continue
		}
		for _, t := range targets {
			if m.sendAutoRule(set, send, r, t, now) {
				// Paced below Telegram's broadcast rate: the sweep sends itself rather
				// than through the notice queue, which drops what does not fit.
				time.Sleep(autoRuleGap)
			}
		}
	}
}

// sendAutoRule writes one message — unless the person moved on meanwhile — and
// reports whether it tried.
func (m *Manager) sendAutoRule(set *model.Settings, send func(int64, string, []model.BroadcastButton) error, r model.AutoRule, t store.AutoRuleTarget, now int64) bool {
	lang := m.userLang(t.ChatID)
	vars := map[string]string{"{name}": escHTML(t.Name), "{code}": "", "{percent}": "", "{until}": ""}
	var code *model.PromoCode
	if t.UserID == 0 && m.RegistrationBlacklisted(t.ChatID) {
		return false
	}
	if t.UserID != 0 {
		u, err := m.store.GetUser(t.UserID)
		if err != nil {
			return false
		}
		// A lapsed user who bought again is no longer lapsed.
		if r.Trigger == model.TriggerLapsed && m.ActivePaidPlan(*u) != nil {
			return false
		}
		if r.DiscountPercent > 0 && set.BillingEnabled {
			code = &model.PromoCode{
				Kind: model.PromoPercent, Value: r.DiscountPercent,
				ExpiresAt: now + int64(r.DiscountDays)*86400,
				Note:      i18n.T(m.botLang(), "promo.ruleNote", r.Name, u.Name),
			}
		}
	}
	attached := false
	if code != nil {
		var err error
		attached, err = m.store.IssueAutoRuleCode(r.ID, t, code, newRuleCode, now)
		if errors.Is(err, store.ErrAutoRuleSent) {
			return false
		}
		if err != nil {
			logErr("auto messages: code not issued", "rule", r.ID, "user", t.UserID, "err", err)
			return false
		}
		vars["{code}"] = escHTML(code.Code)
		vars["{percent}"] = strconv.Itoa(code.Value)
		vars["{until}"] = time.Unix(code.ExpiresAt, 0).In(m.loc()).Format("02.01.2006")
	} else {
		ok, err := m.store.RecordAutoRuleSend(r.ID, t, now)
		if err != nil || !ok {
			return false
		}
	}
	text := r.Text
	for k, v := range vars {
		text = strings.ReplaceAll(text, k, v)
	}
	if code != nil && !strings.Contains(r.Text, "{code}") {
		// Attached, it is already on the next payment; otherwise (another code holds
		// that place) it has to be entered.
		key := "notify.ruleCode"
		if !attached {
			key = "notify.ruleCodeEnter"
		}
		text += "\n\n" + i18n.T(lang, key, code.Value, escHTML(code.Code),
			time.Unix(code.ExpiresAt, 0).In(m.loc()).Format("02.01.2006"))
	}
	if err := send(t.ChatID, text, r.Buttons); err != nil {
		logErr("auto messages: not delivered", "rule", r.ID, "chat", t.ChatID, "err", err)
		// Without a code it is simply tried again next time; a code already given is
		// kept, and the message with it is not repeated.
		if code == nil {
			_ = m.store.ForgetAutoRuleSend(r.ID, t)
		}
		return true
	}
	if t.UserID != 0 {
		data := map[string]any{"rule": r.Name}
		if code != nil {
			data["code"], data["percent"] = code.Code, code.Value
		}
		m.audit(context.Background(), t.UserID, model.EventAutoMessage, data)
	}
	return true
}

func newRuleCode() string {
	return "GIFT" + newWinbackCode()[len("BACK"):]
}
