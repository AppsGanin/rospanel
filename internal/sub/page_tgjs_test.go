package sub

import (
	"github.com/AppsGanin/rospanel/internal/i18n"
	"regexp"
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
	html, err := Page(u, One(set), Billing{}, i18n.RU)
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
	// It must stay a plain BLOCKING script: the inline script at the bottom reads
	// window.Telegram.WebApp synchronously, so defer/async would leave it undefined
	// and silently kill Mini App deep-link routing. Match the whole tag — checking
	// only for `tg.js" defer` misses `<script async src=...>`, where the attribute
	// comes first.
	tag := regexp.MustCompile(`<script[^>]*\btg\.js\b[^>]*>`).FindString(s)
	if tag == "" {
		t.Fatal("no <script> tag for tg.js found")
	}
	if strings.Contains(tag, "async") || strings.Contains(tag, "defer") {
		t.Errorf("tg.js must load synchronously, got %q", tag)
	}
}

// TestPageToleratesMissingSDK guards the degradation path: when the server has no
// cached copy of telegram-web-app.js, /tg.js serves an empty body, so the page must
// read window.Telegram defensively rather than assuming the SDK loaded (an
// unguarded access would throw and take the whole page down).
func TestPageToleratesMissingSDK(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123"}
	set := &model.Settings{Host: "vpn.example.com"}
	html, err := Page(u, One(set), Billing{}, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	// The guarded read + the INTG flag derived from it are what make an empty SDK
	// degrade to "plain browser" instead of a ReferenceError.
	const guard = "var TG = (window.Telegram && window.Telegram.WebApp) || null;"
	if !strings.Contains(s, guard) {
		t.Fatalf("page must read window.Telegram defensively (empty /tg.js is a valid state); want %q", guard)
	}
	if !strings.Contains(s, "var INTG") {
		t.Error("page missing the INTG in-Telegram flag")
	}
	// Every other touch of the SDK must go through TG/INTG. A direct
	// `Telegram.WebApp.…` anywhere else throws on an empty /tg.js and takes the whole
	// page down with it, so assert the guarded read is the ONLY one. (Static check:
	// it can't catch an unguarded `TG.foo()`, which the INTG branches cover.)
	if rest := strings.Replace(s, guard, "", 1); strings.Contains(rest, "Telegram.WebApp") {
		t.Error("unguarded Telegram.WebApp access outside the guarded read — would throw when /tg.js is empty")
	}
}
