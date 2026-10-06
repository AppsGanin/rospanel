package core

import "github.com/AppsGanin/rospanel/internal/model"

// Messages for users the panel's bot does not reach — no Telegram, or the bot off —
// go to the external system as webhooks, which delivers them its own way. Each says
// whether the bot delivered it to Telegram as well (telegram_sent), so a system that
// mirrors everything can tell what the user already saw.

// WebhookWanted reports whether an enabled webhook takes the event.
func (m *Manager) WebhookWanted(event string) bool { return m.webhookWanted(event) }

// EmitUserMessage hands the external system a message the operator wrote to a user:
// Telegram HTML, with its URL buttons and the attachment's kind and name (the file
// itself goes only to Telegram).
func (m *Manager) EmitUserMessage(userID int64, text string, buttons []model.BroadcastButton, mediaKind, mediaName string, telegramSent bool) {
	if buttons == nil {
		buttons = []model.BroadcastButton{}
	}
	extra := map[string]any{"text": text, "buttons": buttons, "telegram_sent": telegramSent}
	if mediaKind != "" {
		extra["media_kind"], extra["media_name"] = mediaKind, mediaName
	}
	m.emitUserWebhook(model.WebhookUserMessage, userID, extra)
}
