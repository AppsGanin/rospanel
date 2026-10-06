package telegram

import (
	"path/filepath"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// pendingPanel answers whether a chat's sign-up waits for the operator.
type pendingPanel struct {
	Panel
	pending bool
}

func (p pendingPanel) RegistrationPending(int64) bool { return p.pending }

// Under moderation, a chat whose request waits is told so on /start — not invited to
// sign up again; one with none gets the welcome and its button.
func TestWelcomeWhileRequestPending(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "welcome.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	set := &model.Settings{TGUserRegMode: model.RegModeration, TGMailingSwitch: true}
	hasRegister := func(rows [][]InlineButton) bool {
		for _, r := range rows {
			for _, b := range r {
				if b.CallbackData == "vu:reg" {
					return true
				}
			}
		}
		return false
	}

	waiting := &UserService{store: st, panel: pendingPanel{pending: true}}
	text, rows := waiting.welcomeScreen(set, 77)
	if text != i18n.T(i18n.RU, "user.requestPending") || hasRegister(rows) {
		t.Errorf("pending: %q, register button %v", text, hasRegister(rows))
	}
	fresh := &UserService{store: st, panel: pendingPanel{}}
	if _, rows := fresh.welcomeScreen(set, 78); !hasRegister(rows) {
		t.Error("no request: the welcome lost its sign-up button")
	}
}
