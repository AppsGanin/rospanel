package core

import (
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The operator's alerts name a user so they can tell which one: a name alone is
// ambiguous — a hundred "Alex" sign up through the bot — so it travels with the
// panel's id and what the person is known by outside it: their @username, else their
// Telegram id, else the external system's id. Escaped for the HTML the bots send.

// adminUser names a user in an operator alert: "Derek (#87 · @derek)".
func (m *Manager) adminUser(u model.User) string {
	parts := []string{"#" + strconv.FormatInt(u.ID, 10)}
	if who := m.telegramHandle(u.TgChatID); who != "" {
		parts = append(parts, who)
	} else if ext := m.store.UserExternalID(u.ID); ext != "" {
		parts = append(parts, ext)
	}
	return escHTML(u.Name) + " (" + escHTML(strings.Join(parts, " · ")) + ")"
}

// adminUserByID is adminUser for what carries an id and a name (an order): the user
// as they are now, or the name it carries once they are gone.
func (m *Manager) adminUserByID(id int64, name string) string {
	if u, err := m.store.GetUser(id); err == nil {
		return m.adminUser(*u)
	}
	return escHTML(name) + " (#" + strconv.FormatInt(id, 10) + ")"
}

// adminChat names someone with no account yet — a sign-up request — by their
// Telegram: "Derek (@derek · tg 6632959743)".
func (m *Manager) adminChat(name string, chatID int64) string {
	parts := []string{}
	if sub, err := m.store.SubscriberByChat(chatID); err == nil && sub != nil && sub.Username != "" {
		parts = append(parts, "@"+sub.Username)
	}
	parts = append(parts, "tg "+strconv.FormatInt(chatID, 10))
	return escHTML(name) + " (" + escHTML(strings.Join(parts, " · ")) + ")"
}

// telegramHandle is how a linked Telegram is recognised: "@username", else "tg <id>";
// "" with none linked.
func (m *Manager) telegramHandle(chatID int64) string {
	if chatID == 0 {
		return ""
	}
	if sub, err := m.store.SubscriberByChat(chatID); err == nil && sub != nil && sub.Username != "" {
		return "@" + sub.Username
	}
	return "tg " + strconv.FormatInt(chatID, 10)
}
