package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A proxy app that sends a browser-like Accept still gets the subscription, not the
// page: Happ 6.x on iOS fetches with "Accept: text/html,…", and the page it used to
// get back has no server links in it.
func TestIsBrowserTrustsKnownClients(t *testing.T) {
	t.Parallel()
	const htmlAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	cases := []struct {
		name, accept, ua string
		want             bool
	}{
		{"safari", htmlAccept, "Mozilla/5.0 (iPhone; CPU iPhone OS 26_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.1 Mobile/15E148 Safari/604.1", true},
		{"chrome", htmlAccept, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36", true},
		{"yandex browser", htmlAccept, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 YaBrowser/25.10.0.0 Safari/537.36", true},
		{"browser without user-agent", htmlAccept, "", true},
		{"happ ios", htmlAccept, "Happ/6.4.0/ios/2611051057585", false},
		{"happ android", htmlAccept, "Happ/6.4.0/Android/2611051057585", false},
		{"v2rayng", htmlAccept, "v2rayNG/1.10.4", false},
		{"v2raytun", htmlAccept, "v2raytun/ios 5.0", false},
		{"hiddify", htmlAccept, "HiddifyNext/2.5.7 (android) like ClashMeta v2ray sing-box", false},
		{"sing-box sfi", htmlAccept, "SFI/1.14.4 (Build 1; sing-box 1.14.4)", false},
		{"karing", htmlAccept, "karing/1.2.0 android", false},
		{"shadowrocket", htmlAccept, "Shadowrocket/2.2.60 CFNetwork/3826 Darwin/25.0.0", false},
		{"mihomo", htmlAccept, "clash.meta/v1.19.32", false},
		{"streisand", htmlAccept, "Streisand/1.6.40", false},
		{"flclash", htmlAccept, "FlClash/v0.8.90 clash-verge Platform/android", false},
		{"browser with meta in its user-agent", htmlAccept, "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Mobile Safari/537.36 MetaBrowser/1.0", true},
		{"no html in accept", "*/*", "Mozilla/5.0 Safari/604.1", false},
		{"no accept at all", "", "Happ/6.4.0/ios", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/sub/x", nil)
		if c.accept != "" {
			req.Header.Set("Accept", c.accept)
		}
		if c.ua != "" {
			req.Header.Set("User-Agent", c.ua)
		}
		if got := isBrowser(req); got != c.want {
			t.Errorf("%s: isBrowser = %v, want %v", c.name, got, c.want)
		}
	}
}

// End to end: the same subscription URL answers a browser with the page and Happ —
// with the very Accept a browser sends — with the links.
func TestSubHappWithBrowserAcceptGetsLinks(t *testing.T) {
	t.Parallel()
	h, mgr, _ := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "happ-ios", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	fetch := func(ua string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/sub/"+u.SubToken, nil)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
		req.Header.Set("User-Agent", ua)
		req.RemoteAddr = testClientIP + ":40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := fetch("Happ/6.4.0/ios/2611051057585")
	if ct := rec.Header().Get("Content-Type"); rec.Code != http.StatusOK || strings.HasPrefix(ct, "text/html") {
		t.Fatalf("happ: %d %q, want the subscription, not the page", rec.Code, ct)
	}
	rec = fetch("Mozilla/5.0 (iPhone; CPU iPhone OS 26_1 like Mac OS X) AppleWebKit/605.1.15 Version/26.1 Mobile/15E148 Safari/604.1")
	if ct := rec.Header().Get("Content-Type"); rec.Code != http.StatusOK || !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("safari: %d %q, want the page", rec.Code, ct)
	}
}
