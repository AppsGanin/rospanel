package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

func bcManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bc.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	// Delivery runs on the user bot's token; without it CreateBroadcast refuses.
	if err := st.SetTelegramUserBot(true, "111:AAA", model.RegOpen, ""); err != nil {
		t.Fatalf("enable user bot: %v", err)
	}
	return &Manager{store: st}
}

// sub links a chat to the audience, optionally to a user account.
func sub(t *testing.T, m *Manager, chatID, userID int64) {
	t.Helper()
	if err := m.store.UpsertSubscriber(chatID, userID, "", "", "", time.Now().Unix()); err != nil {
		t.Fatalf("subscriber %d: %v", chatID, err)
	}
}

func TestValidateBroadcast(t *testing.T) {
	m := bcManager(t)
	sub(t, m, 1, 0)
	ctx := context.Background()

	cases := []struct {
		name string
		b    model.Broadcast
		want string
	}{
		{"empty", model.Broadcast{}, "нечего отправлять"},
		{"unknown audience", model.Broadcast{Text: "hi", Audience: "nobody"}, "неизвестная аудитория"},
		{"unknown media", model.Broadcast{Text: "hi", MediaKind: "video"}, "неизвестный тип вложения"},
		{
			name: "text past the message limit",
			b:    model.Broadcast{Text: strings.Repeat("я", broadcastTextMax+1)},
			want: "длиннее 4096",
		},
		{
			// With media the text becomes a caption, which Telegram cuts at a quarter
			// of the message limit — refusing here beats failing per recipient.
			name: "caption past the media limit",
			b:    model.Broadcast{Text: strings.Repeat("я", broadcastCaptionMax+1), MediaKind: "photo"},
			want: "длиннее 1024",
		},
		{
			name: "button without a link",
			b:    model.Broadcast{Text: "hi", Buttons: []model.BroadcastButton{{Text: "Тык"}}},
			want: "и текст, и ссылка",
		},
		{
			name: "button with a non-http scheme",
			b: model.Broadcast{Text: "hi", Buttons: []model.BroadcastButton{
				{Text: "Тык", URL: "javascript:alert(1)"},
			}},
			want: "http:// или https://",
		},
		{
			name: "too many buttons",
			b: model.Broadcast{Text: "hi", Buttons: make([]model.BroadcastButton,
				broadcastButtonsMax+1)},
			want: "слишком много кнопок",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.b
			_, err := m.CreateBroadcast(ctx, &b)
			if err == nil {
				t.Fatal("accepted an invalid broadcast")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestCreateBroadcastRequiresRecipients(t *testing.T) {
	m := bcManager(t)
	_, err := m.CreateBroadcast(context.Background(),
		&model.Broadcast{Text: "привет", Audience: model.AudienceAll})
	if err == nil || !strings.Contains(err.Error(), "нет получателей") {
		t.Fatalf("error = %v, want a refusal about an empty audience", err)
	}
}

// Delivery goes through the user bot. Creating a broadcast while it is off would
// leave a run stuck at 0 % with nothing explaining why.
func TestCreateBroadcastRequiresUserBot(t *testing.T) {
	m := bcManager(t)
	sub(t, m, 1, 0)
	if err := m.store.SetTelegramUserBot(false, "", model.RegOff, ""); err != nil {
		t.Fatalf("disable user bot: %v", err)
	}
	_, err := m.CreateBroadcast(context.Background(),
		&model.Broadcast{Text: "привет", Audience: model.AudienceAll})
	if err == nil || !strings.Contains(err.Error(), "пользовательского бота") {
		t.Fatalf("error = %v, want a refusal about the user bot", err)
	}
}

func TestAudienceFilters(t *testing.T) {
	m := bcManager(t)
	active := mkUser(t, m, "active", 0)
	expired := mkUser(t, m, "expired", time.Now().Add(-24*time.Hour).Unix())

	sub(t, m, 100, active)
	sub(t, m, 200, expired)
	sub(t, m, 300, 0) // opened the bot, never registered
	// Opted out and blocked chats are excluded from every audience, not just "all".
	sub(t, m, 400, 0)
	if err := m.store.SetSubscriberOptOut(400, true, time.Now().Unix()); err != nil {
		t.Fatalf("opt out: %v", err)
	}
	sub(t, m, 500, 0)
	if err := m.store.SetSubscriberBlocked(500, time.Now().Unix()); err != nil {
		t.Fatalf("block: %v", err)
	}

	for _, tc := range []struct {
		audience string
		want     []int64
	}{
		{model.AudienceAll, []int64{100, 200, 300}},
		{model.AudienceLinked, []int64{100, 200}},
		{model.AudienceUnlinked, []int64{300}},
		{model.AudienceActive, []int64{100}},
		{model.AudienceExpired, []int64{200}},
	} {
		t.Run(tc.audience, func(t *testing.T) {
			got, err := m.audienceChats(tc.audience)
			if err != nil {
				t.Fatalf("audienceChats: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// The audience is a snapshot: a run that re-evaluated itself would pick up people who
// arrived halfway and move the total the progress bar is measured against.
func TestAudienceIsSnapshotted(t *testing.T) {
	m := bcManager(t)
	sub(t, m, 100, 0)
	created, err := m.CreateBroadcast(context.Background(),
		&model.Broadcast{Text: "привет", Audience: model.AudienceAll})
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	if created.Total != 1 {
		t.Fatalf("total = %d, want 1", created.Total)
	}

	sub(t, m, 200, 0) // arrives after the launch
	again, err := m.GetBroadcast(created.ID)
	if err != nil {
		t.Fatalf("GetBroadcast: %v", err)
	}
	if again.Total != 1 {
		t.Fatalf("total moved to %d after a new subscriber joined", again.Total)
	}
}

// A cancelled run must not be revivable: the operator has already decided the
// message should stop going out.
func TestTerminalBroadcastRefusesControl(t *testing.T) {
	m := bcManager(t)
	sub(t, m, 100, 0)
	created, err := m.CreateBroadcast(context.Background(),
		&model.Broadcast{Text: "привет", Audience: model.AudienceAll})
	if err != nil {
		t.Fatalf("CreateBroadcast: %v", err)
	}
	if err := m.SetBroadcastStatus(created.ID, model.BroadcastCancelled); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	err = m.SetBroadcastStatus(created.ID, model.BroadcastRunning)
	if err == nil || !strings.Contains(err.Error(), "уже завершена") {
		t.Fatalf("error = %v, want a refusal to revive a cancelled run", err)
	}
}
