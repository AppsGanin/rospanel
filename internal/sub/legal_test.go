package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// A document is Markdown and nothing more: raw HTML in it is dropped and a
// javascript: link goes nowhere, so the page the panel serves cannot carry a script.
func TestRenderMarkdownIsSafe(t *testing.T) {
	t.Parallel()
	out, err := RenderMarkdown("# Terms\n\n**bold** <script>alert(1)</script> <img src=x onerror=alert(1)>\n\n[x](javascript:alert(1)) [ok](https://example.com)\n\n| a | b |\n|---|---|\n| 1 | 2 |")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, bad := range []string{"<script", "onerror", "javascript:"} {
		if strings.Contains(strings.ToLower(s), bad) {
			t.Errorf("rendered output carries %q:\n%s", bad, s)
		}
	}
	for _, want := range []string{"<h1", "<strong>bold</strong>", `href="https://example.com"`, "<table>"} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered output lacks %q:\n%s", want, s)
		}
	}
}

func TestLegalPageAndURL(t *testing.T) {
	t.Parallel()
	set := &model.Settings{Host: "vpn.example.com", SubPath: "sub", LegalPath: "abc123"}
	if got := LegalURL(set, model.LegalPrivacy); got != "https://vpn.example.com/sub/abc123/privacy" {
		t.Errorf("url = %q", got)
	}
	if LegalURL(&model.Settings{Host: "vpn.example.com"}, model.LegalTerms) != "" {
		t.Error("a URL before the documents have an address")
	}
	page, err := LegalPage(set, model.LegalDoc{Kind: model.LegalTerms, Body: "Hello **world**", UpdatedAt: 1767225600}, i18n.RU, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Пользовательское соглашение", "<strong>world</strong>", "Обновлено 01.01.2026", `name="robots" content="noindex`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// No name set: none shown, not the stock one; a name set: that one.
	if strings.Contains(string(page), `class="brand"`) || strings.Contains(string(page), "РосПанель") {
		t.Error("a page with no service name set names one")
	}
	set.PanelName = "MyVPN"
	named, _ := LegalPage(set, model.LegalDoc{Kind: model.LegalTerms, Body: "x"}, i18n.RU, nil)
	if !strings.Contains(string(named), `<p class="brand">MyVPN</p>`) || !strings.Contains(string(named), "<title>Пользовательское соглашение — MyVPN</title>") {
		t.Error("the service's name is not on its page")
	}
}
