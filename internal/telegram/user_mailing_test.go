package telegram

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The mailing switch sits on the first screen and says what is in force: on by
// default, off once the chat opted out, and its button flips to the other. The bot
// publishes /start alone.
func TestMailingSwitch(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &UserService{store: st}
	const chat = 4040
	if row := s.mailingRow(chat, i18n.RU); len(row) != 1 || row[0].CallbackData != "vu:mail:off" || row[0].Text != i18n.T(i18n.RU, "user.btnMailingOn") {
		t.Fatalf("default: %+v", row)
	}
	if err := st.SetSubscriberOptOut(chat, true, 1); err != nil {
		t.Fatal(err)
	}
	if row := s.mailingRow(chat, i18n.RU); row[0].CallbackData != "vu:mail:on" || row[0].Text != i18n.T(i18n.RU, "user.btnMailingOff") {
		t.Fatalf("opted out: %+v", row)
	}
	// Hidden by the operator: no row at all, shown: the switch.
	if rows := s.mailingRows(&model.Settings{TGMailingSwitch: false}, chat, i18n.RU); rows != nil {
		t.Errorf("switch hidden, yet rows %+v", rows)
	}
	if rows := s.mailingRows(&model.Settings{TGMailingSwitch: true}, chat, i18n.RU); len(rows) != 1 {
		t.Errorf("switch shown: rows %+v", rows)
	}
	if set, _ := st.GetSettings(); !set.TGMailingSwitch {
		t.Error("the switch is hidden by default")
	}
	if cmds := userBotCommands(i18n.RU); len(cmds) != 1 || cmds[0].Command != "start" {
		t.Errorf("commands = %+v, want /start alone", cmds)
	}
}
