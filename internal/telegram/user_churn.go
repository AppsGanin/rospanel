package telegram

import (
	"context"
	"log"
	"strings"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// The churn survey in the user bot: the question with the reasons as buttons
// ("vu:cs:<reason>"); "other" asks for a few words, which the next message carries.

const churnPrefix = "vu:cs:"

// churnRows are the reasons, one per row, in the reader's language.
func churnRows(lang i18n.Lang) [][]InlineButton {
	rows := make([][]InlineButton, 0, len(model.ChurnReasons))
	for _, r := range model.ChurnReasons {
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, sub.ChurnLabelKeys[r]), CallbackData: churnPrefix + r}})
	}
	return rows
}

// askChurn sends the question to a chat.
func (s *UserService) askChurn(ctx context.Context, chatID int64) {
	set, err := s.store.GetSettings()
	if err != nil || strings.TrimSpace(set.TGUserBotToken) == "" {
		return
	}
	lang := s.lang(chatID)
	c := NewClient(strings.TrimSpace(set.TGUserBotToken), set.TelegramProxyURL())
	if err := c.SendMenu(ctx, chatID, i18n.T(lang, "churn.question"), churnRows(lang)); err != nil {
		if isBlockedByUser(err) {
			return
		}
		log.Printf("telegram: churn survey to %d failed: %v", chatID, err)
	}
}

// handleChurnCallback takes a reason pressed; false when data is not one.
func (s *UserService) handleChurnCallback(ctx context.Context, client *Client, chatID, msgID int64, u model.User, data string) bool {
	reason, ok := strings.CutPrefix(data, churnPrefix)
	if !ok {
		return false
	}
	lang := s.lang(chatID)
	if err := s.panel.AnswerChurn(ctx, u.ID, reason, "", "bot"); err != nil {
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "churn.closed"), nil)
		return true
	}
	if reason == model.ChurnOther {
		s.setPending(chatID, "churn")
		s.edit(ctx, client, chatID, msgID, i18n.T(lang, "churn.otherAsk"), nil)
		return true
	}
	s.edit(ctx, client, chatID, msgID, i18n.T(lang, "churn.thanks"), nil)
	return true
}

// doChurnComment takes the words written after "other".
func (s *UserService) doChurnComment(ctx context.Context, client *Client, chatID int64, u model.User, text string) {
	lang := s.lang(chatID)
	if text == "" { // a sticker or a photo: still waiting for the words
		s.setPending(chatID, "churn")
		s.send(ctx, client, chatID, i18n.T(lang, "churn.otherAsk"))
		return
	}
	if err := s.panel.AnswerChurn(ctx, u.ID, model.ChurnOther, text, "bot"); err != nil {
		s.send(ctx, client, chatID, i18n.T(lang, "churn.closed"))
		return
	}
	s.send(ctx, client, chatID, i18n.T(lang, "churn.thanks"))
}
