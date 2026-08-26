package telegram

import (
	"github.com/Shu1t3/rospanel-shu1t3/internal/i18n"
	"testing"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

func TestHumanLeft(t *testing.T) {
	cases := map[int64]string{
		30 * 86400: "осталось 30 дн.",
		2 * 3600:   "осталось 2 ч.",
		45 * 60:    "осталось 45 мин.",
	}
	for sec, want := range cases {
		if got := humanLeft(sec, i18n.RU); got != want {
			t.Errorf("humanLeft(%d, i18n.RU) = %q, want %q", sec, got, want)
		}
	}
}

func TestUserOnlineLine(t *testing.T) {
	now := time.Now().Unix()
	loc := time.UTC
	if got := userOnlineLine(model.User{LastSeen: 0}, now, loc, i18n.RU); got != "🕐 Ещё не подключались" {
		t.Errorf("never-seen: %q", got)
	}
	if got := userOnlineLine(model.User{LastSeen: now - 30}, now, loc, i18n.RU); got != "🟢 Сейчас в сети" {
		t.Errorf("online: %q", got)
	}
	if got := userOnlineLine(model.User{LastSeen: now - 20*60}, now, loc, i18n.RU); got != "🕐 Был в сети 20 мин назад" {
		t.Errorf("mins ago: %q", got)
	}
}

func TestUserStatusLine(t *testing.T) {
	if got := userStatusLine(model.StatusActive, i18n.RU); got != "🟢 <b>Активна</b>" {
		t.Errorf("active: %q", got)
	}
	if got := userStatusLine(model.StatusExpired, i18n.RU); got != "🔴 <b>Срок истёк</b>" {
		t.Errorf("expired: %q", got)
	}
}
