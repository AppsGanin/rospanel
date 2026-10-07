package core

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// signupTelegram is POST /v1/signup for a client known by their Telegram — the
// operator's own bot signing up whoever pressed its start button. It keeps the rules
// the panel's own bot and Mini App keep for a Telegram: their account comes back,
// one they unlinked is restored, the shared blacklist refuses, one trial per
// Telegram for good, and the Telegram is linked to the new account.
func (m *Manager) signupTelegram(ctx context.Context, req SignupRequest, addrKey string) (SignupResult, error) {
	chat := req.TelegramID
	if chat <= 0 {
		return SignupResult{}, invalidCode("err.tgChatInvalid", "Telegram ID пользователя — положительное число")
	}
	if res, ok, err := m.signupByChat(chat); ok || err != nil {
		return res, err
	}
	if set, err := m.Settings(); err == nil && set.RegistrationOpen() && !m.restoresTelegram(chat) {
		if err := m.signupRefused(ctx, model.SignupCheck{
			Channel: "telegram", TelegramID: chat, IP: req.IP, Ref: req.Ref, Source: req.Source, Lang: req.Lang,
		}); err != nil {
			return SignupResult{}, err
		}
	}
	// The lock the Mini App takes: one registration per Telegram at a time.
	m.miniRegMu.Lock()
	defer m.miniRegMu.Unlock()
	if res, ok, err := m.signupByChat(chat); ok || err != nil {
		return res, err
	}
	// Unlinked before: the same account back, not a fresh trial.
	if u, err := m.store.GetDetachedUserByPrevChat(chat); err == nil && u != nil {
		if err := m.store.SetUserTelegramChat(u.ID, chat); err != nil {
			return SignupResult{}, err
		}
		m.AuditTelegramLinked(ctx, u.ID, "")
		u.TgChatID = chat
		return SignupResult{Status: SignupExisting, User: u}, nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SignupResult{}, err
	}
	set, err := m.Settings()
	if err != nil {
		return SignupResult{}, err
	}
	switch {
	case !set.RegistrationOpen():
		return SignupResult{}, invalidCode("err.signupClosed", "регистрация закрыта")
	case m.RegistrationBlacklisted(chat):
		return SignupResult{}, invalidCode("err.signupBlacklisted", "этот Telegram-аккаунт в общем чёрном списке")
	}
	moderation := set.RegMode() == model.RegModeration
	if moderation {
		if r, err := m.store.GetRegistrationRequestByChat(chat); err != nil {
			return SignupResult{}, err
		} else if r != nil {
			return SignupResult{Status: SignupPending, RequestID: r.ID}, nil
		}
	}
	keys := []string{"tg:" + strconv.FormatInt(chat, 10)}
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
	// Kept on the chat, as the bot keeps a /start tag: the account picks them up when
	// it is made — now, or when the operator approves the request.
	if code := strings.TrimPrefix(strings.TrimSpace(req.Ref), "r_"); code != "" {
		m.TrackReferral(chat, code)
	}
	m.TrackSource(chat, req.Source)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "tg-" + strconv.FormatInt(chat, 10)
	}
	if moderation {
		if _, err := m.RequestRegistration(ctx, chat, name); err != nil {
			return SignupResult{}, err
		}
		r, err := m.store.GetRegistrationRequestByChat(chat)
		if err != nil || r == nil {
			return SignupResult{}, errors.Join(errors.New("signup: the request vanished as it was filed"), err)
		}
		if req.Lang != "" {
			_ = m.store.SetRegistrationRequestLang(r.ID, req.Lang)
		}
		return SignupResult{Status: SignupPending, RequestID: r.ID}, nil
	}
	u, err := m.createRegisteredUser(name, !m.store.ChatHadTrial(chat))
	if err != nil {
		return SignupResult{}, err
	}
	if err := m.store.SetUserTelegramChat(u.ID, chat); err != nil {
		// An account its Telegram cannot find again would be made afresh, with a
		// second trial, on the next attempt.
		_ = m.store.DeleteUser(u.ID)
		m.TriggerUserSync()
		return SignupResult{}, err
	}
	u.TgChatID = chat
	_ = m.store.MarkChatTrial(chat)
	m.AttachReferrer(ctx, u.ID, chat)
	m.giveLang(u, req.Lang)
	m.announceRegistration(ctx, u, "", nil)
	m.AuditTelegramLinked(ctx, u.ID, "")
	if fresh, err := m.store.GetUser(u.ID); err == nil {
		u = fresh
	}
	return SignupResult{Status: SignupCreated, User: u}, nil
}

// signupByChat answers a sign-up whose Telegram already has an account.
func (m *Manager) signupByChat(chat int64) (SignupResult, bool, error) {
	u, err := m.store.GetUserByTelegramChatID(chat)
	if errors.Is(err, sql.ErrNoRows) {
		return SignupResult{}, false, nil
	}
	if err != nil {
		return SignupResult{}, false, err
	}
	return SignupResult{Status: SignupExisting, User: u}, true, nil
}
