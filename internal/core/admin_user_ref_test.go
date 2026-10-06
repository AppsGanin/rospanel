package core

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// An operator alert names a user so that one of a hundred "Alex" can be told apart:
// the panel's id and their @username, Telegram id or external id — escaped.
func TestAdminUserNamesTheUser(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "ref.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Manager{store: st, tz: time.UTC}
	a, _ := st.CreateUser("Alex <b>", "uuid-a", "pw", "tok-a", 0, 0, 0)
	b, _ := st.CreateUser("Alex", "uuid-b", "pw", "tok-b", 0, 0, 0)
	c, _ := st.CreateUser("Alex", "uuid-c", "pw", "tok-c", 0, 0, 0)
	if err := st.SetUserTelegramChat(a.ID, 5001); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSubscriber(5001, a.ID, "alex_one", "Alex", "ru", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTelegramChat(b.ID, 5002); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserExternalID(c.ID, "alex@example.com"); err != nil {
		t.Fatal(err)
	}
	ua, _ := st.GetUser(a.ID)
	ub, _ := st.GetUser(b.ID)
	uc, _ := st.GetUser(c.ID)
	for u, want := range map[*model.User]string{
		ua: "Alex &lt;b&gt; (#" + id64(a.ID) + " · @alex_one)",
		ub: "Alex (#" + id64(b.ID) + " · tg 5002)",
		uc: "Alex (#" + id64(c.ID) + " · alex@example.com)",
	} {
		if got := m.adminUser(*u); got != want {
			t.Errorf("adminUser = %q, want %q", got, want)
		}
	}
	if got := m.adminUserByID(99999, "Gone"); got != "Gone (#99999)" {
		t.Errorf("a deleted user: %q", got)
	}

	// A request: the Telegram that asks, where it came from and who invited it.
	if err := st.UpsertSubscriber(7001, 0, "newbie", "Neo", "ru", 1700000000); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSubscriberSource(7001, "vk_ads", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSubscriberRef(7001, b.ID, 1); err != nil {
		t.Fatal(err)
	}
	who, details := m.moderationPrompt(model.RegistrationRequest{ID: 1, ChatID: 7001, Name: "Neo"})
	if who != "Neo (@newbie · tg 7001)" {
		t.Errorf("who = %q", who)
	}
	for _, want := range []string{"vk_ads", "#" + id64(b.ID), "tg 5002"} {
		if !strings.Contains(details, want) {
			t.Errorf("details lack %q:\n%s", want, details)
		}
	}
}

func id64(n int64) string { return strconv.FormatInt(n, 10) }
