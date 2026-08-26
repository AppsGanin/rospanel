package sub

import (
	"html/template"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/i18n"
)

func TestAppRedirectKeepsScheme(t *testing.T) {
	b, err := AppRedirect(template.URL("happ://add/https://vpn.example.com/sub/tok"), i18n.RU)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "happ://add/https://vpn.example.com/sub/tok") {
		t.Fatalf("deep-link scheme not preserved:\n%s", s)
	}
	if strings.Contains(s, "ZgotmplZ") {
		t.Fatalf("scheme was sanitized by html/template:\n%s", s)
	}
}
