package sub

import (
	"github.com/AppsGanin/rospanel/internal/i18n"
	"regexp"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The subscription page must self-host Mulish. Google Fonts is throttled in Russia
// and its stylesheet is render-blocking, so pulling it from there delays paint for
// exactly the users this panel serves — the same class of bug as the old
// telegram.org <script>.
// The tab icon is the operator's own mark, served off the same per-token route the
// page header uses — a panel with a custom logo should look like itself in a pinned
// tab, which is where a subscription page tends to live.
func TestPageFaviconUsesTheBrandingLogo(t *testing.T) {
	u := model.User{Name: "u", SubToken: "tok"}
	set := &model.Settings{Host: "vpn.example.com", SubPath: "sub"}
	html, err := Page(u, set, One(set), Billing{}, Devices{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(html), `rel="icon" href="https://vpn.example.com/sub/tok/logo.svg"`) {
		t.Error("the page has no favicon pointing at the branding logo")
	}
}

func TestPageSelfHostsFonts(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123"}
	set := &model.Settings{Host: "vpn.example.com"}
	html, err := Page(u, set, One(set), Billing{}, Devices{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)

	for _, host := range []string{"fonts.googleapis.com", "fonts.gstatic.com"} {
		if strings.Contains(s, host) {
			t.Errorf("page still references %s — fonts must be served from our own origin", host)
		}
	}
	if !strings.Contains(s, "@font-face") {
		t.Fatal("page has no @font-face rules")
	}
	// html/template rewrites values it distrusts to ZgotmplZ; a CSS url() is exactly
	// the context where that silently breaks every font.
	if strings.Contains(s, "ZgotmplZ") {
		t.Error("html/template rejected a CSS url() value")
	}
}

// Every font the page asks for must actually be embedded. A typo or a rename would
// otherwise 404 at runtime and silently fall back to Arial — invisible in any test
// that only greps for @font-face.
func TestPageFontURLsAreAllEmbedded(t *testing.T) {
	u := model.User{Name: "Ann", SubToken: "tok123"}
	set := &model.Settings{Host: "vpn.example.com"}
	html, err := Page(u, set, One(set), Billing{}, Devices{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	refs := regexp.MustCompile(`/font/([A-Za-z0-9._-]+\.woff2)`).FindAllStringSubmatch(string(html), -1)
	if len(refs) == 0 {
		t.Fatal("page references no font files")
	}
	seen := map[string]bool{}
	for _, m := range refs {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		b, ok := Font(name)
		if !ok {
			t.Errorf("page references %q but it is not embedded", name)
			continue
		}
		// woff2 files start with the "wOF2" signature — guards against embedding a
		// placeholder or a wrong-format file.
		if len(b) < 4 || string(b[:4]) != "wOF2" {
			t.Errorf("%q is not a woff2 file (magic %q)", name, b[:min(4, len(b))])
		}
	}
	// 4 weights x 3 subsets.
	if len(seen) != 12 {
		t.Errorf("page references %d distinct fonts, want 12 (400/600/700/800 x cyrillic/latin/latin-ext)", len(seen))
	}
}

// Font is reachable from a public URL path, so it must never resolve outside fonts/.
func TestFontLookupRejectsTraversal(t *testing.T) {
	for _, bad := range []string{
		"", "../page.html", "../../go.mod", "sub/../../etc/passwd",
		`..\page.html`, "fonts/mulish-latin-400.woff2", // no path separators at all
		"page.html", "mulish-latin-400.woff", // wrong extension
		"nope.woff2", // well-formed but absent
	} {
		if b, ok := Font(bad); ok {
			t.Errorf("Font(%q) returned %d bytes, want refusal", bad, len(b))
		}
	}
	if _, ok := Font("mulish-cyrillic-400.woff2"); !ok {
		t.Error("Font refused a legitimate embedded file")
	}
}
