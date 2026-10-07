package telegram

import (
	"context"
	"errors"
	"strings"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
)

// The plugins' part of the user bot (internal/plugin/bot.go): their buttons under
// the menu, the answers to presses (px:<plugin>:<data>) and to /commands.

// pluginMenuRows are the plugins' menu buttons, two to a row.
func (s *UserService) pluginMenuRows(u model.User, lang i18n.Lang) [][]InlineButton {
	pb := s.panel.PluginBot()
	if pb == nil {
		return nil
	}
	buttons := pb.BotMenu(context.Background(), botUser(u, u.TgChatID, nil), string(lang))
	var rows [][]InlineButton
	for i, b := range buttons {
		if i%2 == 0 {
			rows = append(rows, nil)
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], inlineButton(b))
	}
	return rows
}

// handlePluginCallback answers a press of a plugin's button; false = not one.
func (s *UserService) handlePluginCallback(ctx context.Context, client *Client, cb *CallbackQuery) bool {
	data, ok := strings.CutPrefix(cb.Data, plugin.BotPrefix)
	if !ok {
		return false
	}
	pb := s.panel.PluginBot()
	chatID, msgID := cb.Message.Chat.ID, cb.Message.MessageID
	lang := s.lang(chatID)
	u, linked := s.findLinkedUser(chatID)
	if pb == nil {
		s.pluginGone(ctx, client, chatID, msgID, lang, linked)
		return true
	}
	reply, err := pb.BotCallback(ctx, data, botUser(u, chatID, cb.From), string(lang))
	s.showPluginReply(ctx, client, chatID, msgID, lang, linked, reply, err)
	return true
}

// handlePluginCommand answers a /command a plugin declares; false = none does.
func (s *UserService) handlePluginCommand(ctx context.Context, client *Client, m *Message, cmd string, args []string) bool {
	pb := s.panel.PluginBot()
	name := strings.TrimPrefix(cmd, "/")
	if pb == nil || name == cmd || name == "" {
		return false
	}
	chatID := m.Chat.ID
	lang := s.lang(chatID)
	u, linked := s.findLinkedUser(chatID)
	reply, err := pb.BotCommand(ctx, name, strings.Join(args, " "), botUser(u, chatID, m.From), string(lang))
	if errors.Is(err, plugin.ErrNoBot) {
		return false
	}
	s.showPluginReply(ctx, client, chatID, 0, lang, linked, reply, err)
	return true
}

// showPluginReply shows a plugin's answer — its text escaped, its buttons, and the
// way back to the menu — or "try later" when it failed. msgID 0 sends a new message.
func (s *UserService) showPluginReply(ctx context.Context, client *Client, chatID, msgID int64, lang i18n.Lang, linked bool, reply *model.BotReply, err error) {
	if err != nil || reply == nil || strings.TrimSpace(reply.Text) == "" {
		s.pluginGone(ctx, client, chatID, msgID, lang, linked)
		return
	}
	var rows [][]InlineButton
	for _, r := range reply.Buttons {
		var row []InlineButton
		for _, b := range r {
			row = append(row, inlineButton(b))
		}
		rows = append(rows, row)
	}
	if linked {
		rows = append(rows, []InlineButton{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}})
	}
	if msgID == 0 {
		s.sendMenu(ctx, client, chatID, esc(reply.Text), rows)
		return
	}
	s.edit(ctx, client, chatID, msgID, esc(reply.Text), rows)
}

func (s *UserService) pluginGone(ctx context.Context, client *Client, chatID, msgID int64, lang i18n.Lang, linked bool) {
	var rows [][]InlineButton
	if linked {
		rows = [][]InlineButton{{{Text: i18n.T(lang, "user.btnMenu"), CallbackData: "vu:menu"}}}
	}
	if msgID == 0 {
		s.sendMenu(ctx, client, chatID, i18n.T(lang, "user.pluginUnavailable"), rows)
		return
	}
	s.edit(ctx, client, chatID, msgID, i18n.T(lang, "user.pluginUnavailable"), rows)
}

// pluginCommands are the plugins' commands for the command menu.
func (s *UserService) pluginCommands(lang i18n.Lang) []BotCommand {
	pb := s.panel.PluginBot()
	if pb == nil {
		return nil
	}
	var out []BotCommand
	for _, c := range pb.BotCommands(string(lang)) {
		out = append(out, BotCommand{Command: c.Command, Description: c.Description})
	}
	return out
}

func botUser(u model.User, chatID int64, from *User) model.BotUser {
	b := model.BotUser{ID: u.ID, Name: u.Name, TelegramID: chatID}
	if from != nil {
		b.Username = from.Username
		if b.Name == "" {
			b.Name = from.FirstName
		}
	}
	return b
}

func inlineButton(b model.BotButton) InlineButton {
	if b.URL != "" {
		return InlineButton{Text: b.Text, URL: b.URL}
	}
	return InlineButton{Text: b.Text, CallbackData: b.Data}
}
