package telegram

import (
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

func TestHumanLeft(t *testing.T) {
	cases := map[int64]string{
		30 * 86400: "осталось 30 дн.",
		2 * 3600:   "осталось 2 ч.",
		45 * 60:    "осталось 45 мин.",
	}
	for sec, want := range cases {
		if got := humanLeft(sec); got != want {
			t.Errorf("humanLeft(%d) = %q, want %q", sec, got, want)
		}
	}
}

func TestUserOnlineLine(t *testing.T) {
	now := time.Now().Unix()
	loc := time.UTC
	if got := userOnlineLine(model.User{LastSeen: 0}, now, loc); got != "🕐 Ещё не подключались" {
		t.Errorf("never-seen: %q", got)
	}
	if got := userOnlineLine(model.User{LastSeen: now - 30}, now, loc); got != "🟢 Сейчас в сети" {
		t.Errorf("online: %q", got)
	}
	if got := userOnlineLine(model.User{LastSeen: now - 20*60}, now, loc); got != "🕐 Был в сети 20 мин назад" {
		t.Errorf("mins ago: %q", got)
	}
}

func TestUserStatusLine(t *testing.T) {
	if got := userStatusLine(model.StatusActive); got != "🟢 <b>Активна</b>" {
		t.Errorf("active: %q", got)
	}
	if got := userStatusLine(model.StatusExpired); got != "🔴 <b>Срок истёк</b>" {
		t.Errorf("expired: %q", got)
	}
}
