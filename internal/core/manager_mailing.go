package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// An account's mailing switch and language, as the API, the operator and the bot set
// them. Mailings — broadcasts and automatic messages, through the bot or the external
// system — skip an account with them off; service notices still go.

// contactLang is the language to write to an account in: its own, else what its
// Telegram reports; "" when neither is known.
func contactLang(c store.UserContact) string {
	switch {
	case c.OwnLang != "":
		return c.OwnLang
	case c.ChatLang != "":
		return string(i18n.Normalize(c.ChatLang))
	}
	return ""
}

// UserContact is how an account is reached: mailing on or off, and its language.
func (m *Manager) UserContact(id int64) (mailing bool, lang string, err error) {
	cs, err := m.store.UserContacts([]int64{id})
	if err != nil {
		return false, "", err
	}
	c := cs[id]
	return !c.MailingOff, contactLang(c), nil
}

// Contact is how one account is reached, as the views show it.
type Contact struct {
	ExternalID string
	Mailing    bool
	Lang       string
}

// UserContacts is UserContact for many accounts, read at once.
func (m *Manager) UserContacts(ids []int64) (map[int64]Contact, error) {
	cs, err := m.store.UserContacts(ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Contact, len(cs))
	for id, c := range cs {
		out[id] = Contact{ExternalID: c.ExternalID, Mailing: !c.MailingOff, Lang: contactLang(c)}
	}
	return out, nil
}

// SetUserMailing switches mailings for an account (and its Telegram chat). Always
// written — the account's own switch must hold even when its chat already says the
// same, since the chat may leave it — and reported when the state moved.
func (m *Manager) SetUserMailing(ctx context.Context, id int64, on bool) error {
	was, _, err := m.UserContact(id)
	if err != nil {
		return err
	}
	err = m.store.SetUserMailing(id, !on)
	if errors.Is(err, sql.ErrNoRows) {
		return invalidCode("err.userNotFound", "пользователь не найден")
	}
	if err != nil {
		return err
	}
	if was != on {
		m.mailingChanged(ctx, id, on)
	}
	return nil
}

// SetChatMailing is the bot's switch: the chat, and the account holding it. An old
// message's button, or a second tap, changes nothing and reports nothing.
func (m *Manager) SetChatMailing(ctx context.Context, chatID int64, on bool) error {
	wasOff, err := m.store.ChatMailingOff(chatID)
	if err != nil {
		return err
	}
	userID, err := m.store.SetChatMailing(chatID, !on, time.Now().Unix())
	if err != nil {
		return err
	}
	if userID != 0 && wasOff == on {
		m.mailingChanged(ctx, userID, on)
	}
	return nil
}

func (m *Manager) mailingChanged(ctx context.Context, id int64, on bool) {
	m.audit(ctx, id, model.EventUserMailing, map[string]any{"mailing": on})
	m.emitUserWebhook(model.WebhookUserMailing, id, nil)
}

// SetUserLang sets an account's language: ru, en, or "" for what Telegram reports.
func (m *Manager) SetUserLang(ctx context.Context, id int64, lang string) error {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch lang {
	case "", string(i18n.RU), string(i18n.EN):
	default:
		return invalidCode("err.userLang", "язык — ru или en")
	}
	err := m.store.SetUserLang(id, lang)
	if errors.Is(err, sql.ErrNoRows) {
		return invalidCode("err.userNotFound", "пользователь не найден")
	}
	return err
}
