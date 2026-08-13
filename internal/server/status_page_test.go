package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// While the page is off, its path is indistinguishable from any other unknown one
// — that is the whole reason it ships disabled.
func TestStatusPageHiddenUntilEnabled(t *testing.T) {
	h, _, _ := nodeAPITestServer(t)

	off := getFrom(h, "/status")
	unknown := getFrom(h, "/no-such-path")
	if off.Code != unknown.Code || off.Body.String() != unknown.Body.String() {
		t.Errorf("a disabled status page answers differently from an unknown path (%d vs %d)",
			off.Code, unknown.Code)
	}
}

func TestStatusPageRendersWhenEnabled(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	if err := st.SetStatusPage(true, "status"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	h.(*Router).setStatusPath("status")

	// Some history to render: yesterday dipped (95% of samples up — a partial day,
	// not an outage), today fine.
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	for range 19 {
		if err := st.RecordUptimeSample(model.LocalNodeID, yesterday, true); err != nil {
			t.Fatalf("sample: %v", err)
		}
	}
	if err := st.RecordUptimeSample(model.LocalNodeID, yesterday, false); err != nil {
		t.Fatalf("sample: %v", err)
	}
	if err := st.RecordUptimeSample(model.LocalNodeID, today, true); err != nil {
		t.Fatalf("sample: %v", err)
	}

	rec := getFrom(h, "/status")
	if rec.Code != http.StatusOK {
		t.Fatalf("status page: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "noindex") {
		t.Error("the page invites indexing")
	}
	if !strings.Contains(body, "bar partial") {
		t.Error("a day with a dip isn't rendered as a partial outage")
	}
	// The page must not carry anything an outsider could use to map the install.
	set, _ := st.GetSettings()
	for _, secret := range []string{set.PanelSecretPath, set.NodeAPIPath, set.SubPathOr()} {
		if secret != "" && strings.Contains(body, secret) {
			t.Errorf("the status page leaks the %q segment", secret)
		}
	}
	if _, err := mgr.Settings(); err != nil {
		t.Fatalf("settings: %v", err)
	}

	// The operator's own mark, served from the status path itself — the page has no
	// token to hang a per-user asset off, so this is the one sub-resource it has. It
	// is the tab icon rather than a header: the body carries no branding at all.
	if !strings.Contains(body, `rel="icon" href="/status/logo.svg"`) {
		t.Error("the page has no favicon pointing at the operator's mark")
	}
	if strings.Contains(body, `class="head"`) || strings.Contains(body, "<h1") {
		t.Error("the page still renders a branding header")
	}
	logo := getFrom(h, "/status/logo.svg")
	if logo.Code != http.StatusOK || logo.Body.Len() == 0 {
		t.Errorf("logo: status %d, %d bytes", logo.Code, logo.Body.Len())
	}
	if ct := logo.Header().Get("Content-Type"); !strings.Contains(ct, "image") &&
		!strings.Contains(ct, "svg") {
		t.Errorf("logo Content-Type = %q", ct)
	}

	// Every OTHER sub-path is not the page: an endless supply of URLs that all confirm
	// the panel is exactly what the masquerade is there to prevent.
	if sub := getFrom(h, "/status/anything"); sub.Code == http.StatusOK &&
		strings.Contains(sub.Body.String(), "noindex") {
		t.Error("/status/<anything> renders the status page")
	}
}

func TestStatusPathProblem(t *testing.T) {
	set := &model.Settings{PanelSecretPath: "secret123", SubPath: "sub", APIPath: "apiseg"}
	for _, c := range []struct{ path, wantCode string }{
		{"status", ""},
		{"uptime-page_2", ""},
		{"bad path", "err.badStatusPath"},
		{"слэш", "err.badStatusPath"},
		{"secret123", "err.statusPathTaken"},
		{"sub", "err.statusPathTaken"},
		{"apiseg", "err.statusPathTaken"},
	} {
		code, _ := statusPathProblem(c.path, set)
		if code != c.wantCode {
			t.Errorf("statusPathProblem(%q) = %q, want %q", c.path, code, c.wantCode)
		}
	}
}
