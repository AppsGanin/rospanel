package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// fetchSubUA fetches a subscription with a chosen User-Agent (fetchSub hardcodes one).
func fetchSubUA(h http.Handler, token, ua string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/sub/"+token, nil)
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Response rules override the automatic format detection: a rule can force a format
// for a specific client, or block one entirely (served the decoy). This drives the
// whole path — store, evaluate, serve — not just the pure evaluator.
func TestSubscriptionResponseRules(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "sub-rules", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_ = st
	if err := mgr.SaveSubRules([]model.SubRule{
		// A plain browser UA that would normally get v2ray links is forced to sing-box.
		{Field: model.SubMatchUserAgent, Op: model.SubOpContains, Value: "forceme",
			Action: model.SubActionSingbox, Enabled: true},
		// A scraper is blocked outright.
		{Field: model.SubMatchUserAgent, Op: model.SubOpContains, Value: "curl",
			Action: model.SubActionBlock, Enabled: true},
	}); err != nil {
		t.Fatalf("save rules: %v", err)
	}

	// Forced format: sing-box is JSON, not the plain link list this UA would get.
	rec := fetchSubUA(h, u.SubToken, "forceme/1.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("forced fetch: status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("forced client got %q, want sing-box JSON", ct)
	}

	// Blocked: served the decoy, which must NOT contain the user's real config.
	blocked := fetchSubUA(h, u.SubToken, "curl/8.4")
	if strings.Contains(blocked.Body.String(), u.UUID) {
		t.Error("a blocked client received the real subscription")
	}

	// A client matching no rule falls through to auto-detection (a plain UA → links).
	normal := fetchSubUA(h, u.SubToken, "Mozilla/5.0")
	if ct := normal.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("unmatched client got %q, want the auto-detected link list", ct)
	}

	// A bad rule (uncompilable regex) is refused on save.
	if err := mgr.SaveSubRules([]model.SubRule{
		{Field: model.SubMatchUserAgent, Op: model.SubOpRegex, Value: "(", Action: model.SubActionClash, Enabled: true},
	}); err == nil {
		t.Error("a rule with an invalid regex was accepted")
	}
}

// A rule may only change protocols: the format stays the detected one, a protocol it
// hides is gone, and a saved rule drops overrides a blocking one could never use.
func TestSubscriptionRuleProtocols(t *testing.T) {
	t.Parallel()
	h, mgr, _ := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "sub-proto", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	links := func(ua string) string {
		rec := fetchSubUA(h, u.SubToken, ua)
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
			t.Fatalf("%s: got %q, want the link list", ua, ct)
		}
		b, err := base64.StdEncoding.DecodeString(rec.Body.String())
		if err != nil {
			return rec.Body.String()
		}
		return string(b)
	}
	if !strings.Contains(links("Happ/3.8.1"), "hysteria2://") {
		t.Fatal("no Hysteria2 link to begin with")
	}
	if err := mgr.SaveSubRules([]model.SubRule{
		{Field: model.SubMatchUserAgent, Op: model.SubOpContains, Value: "happ", Action: model.SubActionAuto,
			Enabled: true, Protocols: map[string]string{model.LaneHysteria: model.SubProtoOff}},
		{Field: model.SubMatchUserAgent, Op: model.SubOpContains, Value: "curl", Action: model.SubActionBlock,
			Enabled: true, Protocols: map[string]string{model.LaneMieru: model.SubProtoOn}},
	}); err != nil {
		t.Fatalf("save rules: %v", err)
	}
	got := links("Happ/3.8.1")
	if strings.Contains(got, "hysteria2://") || !strings.Contains(got, "vless://") {
		t.Errorf("the rule did not hide only Hysteria2:\n%s", got)
	}
	if !strings.Contains(links("v2rayN/7.15.0"), "hysteria2://") {
		t.Error("a client the rule does not match lost Hysteria2")
	}
	rules, _ := mgr.SubRules()
	if rules[1].Protocols != nil {
		t.Errorf("a blocking rule kept protocol overrides: %v", rules[1].Protocols)
	}
	if err := mgr.SaveSubRules([]model.SubRule{{Field: model.SubMatchUserAgent, Op: model.SubOpContains, Value: "x",
		Action: model.SubActionAuto, Enabled: true, Protocols: map[string]string{"bogus": model.SubProtoOn}}}); err == nil {
		t.Error("an unknown protocol was accepted")
	}
}
