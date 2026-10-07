package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

const botManifest = `{
	"id": "botty", "version": "1.0.0", "api": 1, "name": "Botty",
	"provides": {"bot": {"menu": true, "commands": [
		{"command": "bonus", "description": {"ru": "Бонус", "en": "Bonus"}}
	]}}
}`

const botMain = `
export const bot = {
	menu({ user, lang }) {
		return [
			{ text: "Bonus for " + user.id, data: "bonus" },
			{ text: "Site", url: "https://example.com" },
			{ text: "Evil", url: "javascript:alert(1)" },
			{ text: "Long", data: "x".repeat(80) },
			{ text: "", data: "empty" },
		];
	},
	onCallback({ user, data }) {
		return { text: "<b>pressed</b> " + data, buttons: [[{ text: "Again", data: "again" }], { text: "Docs", url: "https://docs.example" }] };
	},
	onCommand({ command, args }) { return "ran " + command + " " + args; },
};
`

func TestBotMenuCallbackCommand(t *testing.T) {
	h := newHarness(t)
	h.install(pkg(t, botManifest, botMain, nil))
	h.enable("botty")
	ctx := context.Background()
	u := model.BotUser{ID: 5, TelegramID: 500}

	menu := h.host.BotMenu(ctx, u, "en")
	if len(menu) != 2 || menu[0].Data != "px:botty:bonus" || menu[0].Text != "Bonus for 5" || menu[1].URL != "https://example.com" {
		t.Fatalf("menu: %+v", menu)
	}
	r, err := h.host.BotCallback(ctx, "botty:bonus", u, "en")
	if err != nil || r.Text != "<b>pressed</b> bonus" || len(r.Buttons) != 2 || r.Buttons[0][0].Data != "px:botty:again" || r.Buttons[1][0].URL == "" {
		t.Fatalf("callback: %+v %v", r, err)
	}
	if _, err := h.host.BotCallback(ctx, "nobody:x", u, "en"); !errors.Is(err, ErrNoBot) {
		t.Fatalf("a press for no plugin: %v", err)
	}
	r, err = h.host.BotCommand(ctx, "bonus", "a b", u, "en")
	if err != nil || r.Text != "ran bonus a b" {
		t.Fatalf("command: %+v %v", r, err)
	}
	if _, err := h.host.BotCommand(ctx, "other", "", u, "en"); !errors.Is(err, ErrNoBot) {
		t.Fatalf("an undeclared command: %v", err)
	}
	cmds := h.host.BotCommands("ru")
	if len(cmds) != 1 || cmds[0] != (model.BotCommandInfo{Command: "bonus", Description: "Бонус"}) {
		t.Fatalf("commands: %+v", cmds)
	}
}

func TestBotCommandsNeedDescriptions(t *testing.T) {
	h := newHarness(t)
	bad := strings.Replace(botManifest, `"description": {"ru": "Бонус", "en": "Bonus"}`, `"description": ""`, 1)
	if _, err := h.host.Inspect(pkg(t, bad, botMain, nil)); err == nil || !strings.Contains(err.Error(), "description: required") {
		t.Fatalf("a command without a description: %v", err)
	}
}
