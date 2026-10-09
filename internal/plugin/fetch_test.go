package plugin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestDeniedAddr(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "127.8.8.8", "::1", "10.0.0.5", "172.16.3.4", "192.168.1.1", "169.254.169.254",
		"100.64.1.1", "0.0.0.0", "::", "fe80::1", "fc00::1", "fd12::1", "224.0.0.1", "ff02::1",
		"64:ff9b::7f00:1", "2002:7f00:1::", "198.18.0.1", "255.255.255.255",
	} {
		if !deniedAddr(netip.MustParseAddr(s).Unmap()) {
			t.Errorf("%s is reachable", s)
		}
	}
	// An IPv4-mapped IPv6 loopback is unmapped before the check.
	if !deniedAddr(netip.MustParseAddr("::ffff:127.0.0.1").Unmap()) {
		t.Error("::ffff:127.0.0.1 is reachable")
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "144.31.159.81"} {
		if deniedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s is refused", s)
		}
	}
}

func TestAllowlist(t *testing.T) {
	allow := []string{"api.example.com", "*.cdn.net", "pay.io:8443"}
	for raw, want := range map[string]bool{
		"https://api.example.com/x":         true,
		"http://api.example.com:8080/x":     false, // a bare name is 80 and 443 only
		"http://api.example.com/x":          true,
		"https://API.EXAMPLE.COM/x":         true,
		"https://evil.example.com/x":        false,
		"https://api.example.com.evil.io/x": false,
		"https://a.cdn.net/x":               true,
		"https://a.b.cdn.net/x":             true,
		"https://cdn.net/x":                 false, // the wildcard is for subdomains
		"https://xcdn.net/x":                false,
		"https://pay.io:8443/x":             true,
		"https://pay.io/x":                  false,
	} {
		u, _ := url.Parse(raw)
		if got := allowed(u, allow); got != want {
			t.Errorf("%s: %v, want %v", raw, got, want)
		}
	}
}

// The real dialer refuses loopback even for an allowlisted name.
func TestFetchRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secret") }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	f := NewFetcher()
	_, err := f.Fetch(context.Background(), []string{"localhost.test"}, FetchRequest{URL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("by IP literal: %v", err)
	}
	// "localhost" resolves to 127.0.0.1: allowlisted, still refused at the dial.
	_, err = f.Fetch(context.Background(), []string{"localhost:" + u.Port()}, FetchRequest{URL: "http://localhost:" + u.Port()})
	if err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatalf("by name: %v", err)
	}
}

func TestFetchRequestAndRedirects(t *testing.T) {
	var gotUA, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/echo":
			gotUA, gotCT = r.Header.Get("User-Agent"), r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.Header().Set("X-Reply", "1")
			_, _ = io.WriteString(w, `{"ok":true}`)
		case "/away":
			http.Redirect(w, r, "http://evil.test/steal", http.StatusFound)
		case "/big":
			_, _ = io.WriteString(w, strings.Repeat("x", maxFetchResponse+10))
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	f := NewFetcher()
	f.denied = func(netip.Addr) bool { return false } // reach httptest
	allow := []string{"localhost:" + u.Port()}
	base := "http://localhost:" + u.Port()

	r, err := f.Fetch(context.Background(), allow, FetchRequest{URL: base + "/echo", Method: "post", Body: `{"a":1}`,
		Headers: map[string]string{"Content-Type": "application/json"}})
	if err != nil || r.Status != 200 || r.Body != `{"ok":true}` || r.Headers["x-reply"] != "1" {
		t.Fatalf("%+v %v", r, err)
	}
	if gotUA != "RosPanel-Plugin/1" || gotCT != "application/json" || gotBody != `{"a":1}` {
		t.Fatalf("request: %q %q %q", gotUA, gotCT, gotBody)
	}
	if _, err := f.Fetch(context.Background(), allow, FetchRequest{URL: base + "/away"}); err == nil || !strings.Contains(err.Error(), "redirect to evil.test") {
		t.Fatalf("redirect off the allowlist: %v", err)
	}
	if _, err := f.Fetch(context.Background(), allow, FetchRequest{URL: base + "/big"}); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("oversized answer: %v", err)
	}
	for _, bad := range []string{"file:///etc/passwd", "ftp://localhost/x", "http://user:pw@localhost/", "localhost"} {
		if _, err := f.Fetch(context.Background(), allow, FetchRequest{URL: bad}); err == nil {
			t.Errorf("fetched %s", bad)
		}
	}
}

// A blob body streams out with its length, and an answer streams into a sink.
func TestFetchStreamsBlobs(t *testing.T) {
	var gotLen int64
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotLen, got = r.ContentLength, string(b)
		_, _ = io.WriteString(w, strings.Repeat("z", 5<<20)) // past the 4 MB a heap answer may have
	}))
	defer srv.Close()
	f := NewFetcher()
	f.denied = func(netip.Addr) bool { return false }
	host := strings.TrimPrefix(srv.URL, "http://")
	var sink strings.Builder
	resp, err := f.Fetch(context.Background(), []string{host}, FetchRequest{
		URL: srv.URL, Method: "PUT", BodyReader: strings.NewReader("blob!"), BodySize: 5, Sink: &sink, MaxBytes: 8 << 20,
	})
	if err != nil || resp.Status != 200 || sink.Len() != 5<<20 || resp.Body != "" {
		t.Fatalf("%+v %v (sink %d)", resp, err, sink.Len())
	}
	if gotLen != 5 || got != "blob!" {
		t.Fatalf("server got %d %q", gotLen, got)
	}
}

// A failed request does not echo the URL: tokens ride in paths and queries.
func TestFetchErrorHidesTheURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	u, _ := url.Parse(srv.URL)
	srv.Close() // nothing listens there now
	f := NewFetcher()
	f.denied = func(netip.Addr) bool { return false }
	_, err := f.Fetch(context.Background(), []string{"localhost:" + u.Port()},
		FetchRequest{URL: "http://localhost:" + u.Port() + "/bot123:SECRET/send?key=SECRET2"})
	if err == nil || strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "localhost") {
		t.Fatalf("error: %v", err)
	}
}
