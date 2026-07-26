package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// TestPageServesTelegramSDKLocally guards the Russia-block fix: the subscription
// page must load the Telegram Mini App SDK from our own origin (<SubURL>/tg.js),
// never straight from telegram.org — a direct render-blocking <script> to that
// (blocked) host hangs the page before it paints.
func TestPageServesTelegramSDKLocally(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123"}
	set := &model.Settings{Host: "vpn.example.com"}
	html, err := Page(u, One(set), Billing{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	if strings.Contains(s, "telegram.org") {
		t.Error("page still references telegram.org — it must load the SDK from our own origin")
	}
	if !strings.Contains(s, `/tg.js"></script>`) {
		t.Error("page missing the same-origin <script src=.../tg.js>")
	}
	// It must stay a plain blocking script: the inline script at the bottom reads
	// window.Telegram.WebApp synchronously, so defer/async would break the ordering.
	if strings.Contains(s, "/tg.js\" defer") || strings.Contains(s, "/tg.js\" async") {
		t.Error("tg.js must load synchronously, not deferred/async")
	}
}

// TestPageToleratesMissingSDK guards the degradation path: when the server has no
// cached copy of telegram-web-app.js, /tg.js serves an empty body, so the page must
// read window.Telegram defensively rather than assuming the SDK loaded (an
// unguarded access would throw and take the whole page down).
func TestPageToleratesMissingSDK(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123"}
	set := &model.Settings{Host: "vpn.example.com"}
	html, err := Page(u, One(set), Billing{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	// The guarded read + the INTG flag derived from it are what make an empty SDK
	// degrade to "plain browser" instead of a ReferenceError.
	if !strings.Contains(s, "window.Telegram && window.Telegram.WebApp") {
		t.Error("page must read window.Telegram defensively (empty /tg.js is a valid state)")
	}
	if !strings.Contains(s, "var INTG") {
		t.Error("page missing the INTG in-Telegram flag")
	}
}
