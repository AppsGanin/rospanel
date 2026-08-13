package sub

import (
	"regexp"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// cyrillic matches any Russian letter.
var cyrillic = regexp.MustCompile(`[А-Яа-яЁё]`)

func langTestServers() []Server {
	return []Server{{Set: &model.Settings{
		Host:           "vpn.example.com",
		SubPath:        "sub",
		VLESSEnabled:   true,
		RealityEnabled: true,
		// What a real install has: the individual-config card is on by default, and
		// this test reads the strings it renders.
		SubShowConfigs: true,
	}}}
}

// TestPageEnglishHasNoRussian is the Go-side guard against the bug the frontend
// had: a translation that silently falls back to the reference language leaves
// Russian sitting in an otherwise English page, and nothing fails to compile.
//
// The user name is deliberately Latin here — the check is about the page's own
// chrome, not about operator-entered content, which is passed through verbatim in
// whatever language it was typed.
func TestPageEnglishHasNoRussian(t *testing.T) {
	u := model.User{Name: "Alice", SubToken: "tok", DataLimit: 5 << 30, UsedUp: 1 << 30}
	html, err := Page(u, langTestServers(), Billing{}, Devices{}, true, i18n.EN)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, line := range strings.Split(string(html), "\n") {
		if cyrillic.MatchString(line) {
			t.Errorf("Russian leaked into the English page:\n  %s", strings.TrimSpace(line))
		}
	}
}

// TestPageRussianStillRussian is the other half: the English work must not have
// quietly turned the Russian page English.
func TestPageRussianStillRussian(t *testing.T) {
	u := model.User{Name: "Алиса", SubToken: "tok"}
	html, err := Page(u, langTestServers(), Billing{}, Devices{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	for _, want := range []string{"Привет", "Скопировать ссылку подписки", "Отдельные конфиги"} {
		if !strings.Contains(s, want) {
			t.Errorf("Russian page is missing %q", want)
		}
	}
}

// TestPageSwitchesWithLanguage pins that the two renders actually differ where it
// matters — a page that ignored its lang argument would pass both tests above only
// if the catalogs happened to agree, so assert a known string flips.
func TestPageSwitchesWithLanguage(t *testing.T) {
	u := model.User{Name: "Alice", SubToken: "tok"}
	en, err := Page(u, langTestServers(), Billing{}, Devices{}, true, i18n.EN)
	if err != nil {
		t.Fatalf("render en: %v", err)
	}
	if !strings.Contains(string(en), "Copy the subscription link") {
		t.Error("English page does not carry the English copy-link label")
	}
}

// TestAppRedirectLocalised covers the deep-link hand-off page, which is rendered
// from its own template and was easy to forget.
func TestAppRedirectLocalised(t *testing.T) {
	for _, tc := range []struct {
		lang i18n.Lang
		want string
	}{
		{i18n.EN, "Opening the app"},
		{i18n.RU, "Открываем приложение"},
	} {
		b, err := AppRedirect("happ://add/x", tc.lang)
		if err != nil {
			t.Fatalf("%s: %v", tc.lang, err)
		}
		if !strings.Contains(string(b), tc.want) {
			t.Errorf("%s redirect page missing %q:\n%s", tc.lang, tc.want, b)
		}
	}
	b, _ := AppRedirect("happ://add/x", i18n.EN)
	if cyrillic.MatchString(string(b)) {
		t.Errorf("Russian leaked into the English redirect page:\n%s", b)
	}
}
