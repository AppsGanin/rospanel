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
	html, err := Page(u, set, One(set), Billing{}, Devices{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	// What must not happen is a LOAD from telegram.org. The name may legitimately
	// appear otherwise — the SDK-free fallback posts to "https://web.telegram.org" as
	// a targetOrigin, which fetches nothing and is what stops the message reaching
	// anyone else.
	if load := regexp.MustCompile(`(?i)(src|href)\s*=\s*["'][^"']*telegram\.org`).FindString(s); load != "" {
		t.Errorf("page loads from telegram.org (%q) — it must come from our own origin", load)
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
	html, err := Page(u, set, One(set), Billing{}, Devices{}, true, i18n.RU)
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

// Issue #43: inside Telegram, the page must work even when the SDK never arrived.
//
// /tg.js is telegram.org's SDK proxied through the panel, and that fetch fails
// exactly where this panel is used. The page then had no window.Telegram, decided it
// was an ordinary browser, and let the app buttons fire their custom scheme — which
// Telegram's webview cannot launch. The buttons did nothing, in the one context that
// needs them most.
//
// So Telegram must be recognised by signals the SDK doesn't own, and the calls the
// page makes must have a path that doesn't go through it.
func TestPageDetectsTelegramWithoutTheSDK(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123"}
	set := &model.Settings{Host: "vpn.example.com"}
	html, err := Page(u, set, One(set), Billing{}, Devices{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)

	// The two SDK-free signals: the injected proxy object on mobile, and the
	// tgWebApp* parameters Telegram appends to a Mini App's launch URL.
	for _, signal := range []string{"TelegramWebviewProxy", "tgWebApp"} {
		if !strings.Contains(s, signal) {
			t.Errorf("page cannot detect Telegram without the SDK: no %s check", signal)
		}
	}
	// INTG must not be decided by the SDK alone any more.
	if regexp.MustCompile(`var INTG = [^;]*;`).FindString(s) == "var INTG = !!(TG && TG.platform && TG.platform !== \"unknown\");" {
		t.Error("INTG still depends only on the SDK — an empty /tg.js puts it back to false inside Telegram")
	}
	// The host messages the page falls back to. Without these it can detect Telegram
	// and still be unable to ask it for anything.
	for _, event := range []string{"web_app_open_link", "web_app_open_tg_link", "web_app_expand"} {
		if !strings.Contains(s, event) {
			t.Errorf("no SDK-free path for %s", event)
		}
	}
	// A tap must never be swallowed: if no host answers, the page navigates itself.
	if !strings.Contains(s, "if (!tgOpen(bounce)) window.location = bounce;") {
		t.Error("app taps have no fallback when nothing answers — the button would do nothing")
	}
}
