package netguard

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The operator types this by hand into a settings field, so the parser is judged on
// what it does with a plausible typo as much as on what it accepts.
func TestParseProxy(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		ok   bool
		// want is a fragment the error must contain — the message is the whole point
		// of rejecting, so a rejection with unhelpful wording is a failure too.
		want string
	}{
		{name: "empty means direct", raw: "", ok: true},
		{name: "blank means direct", raw: "   ", ok: true},
		{name: "socks5", raw: "socks5://127.0.0.1:1080", ok: true},
		{name: "socks5h", raw: "socks5h://proxy.example:1080", ok: true},
		{name: "credentials", raw: "socks5://user:pass@10.0.0.5:1080", ok: true},
		{name: "http with port", raw: "http://10.0.0.5:3128", ok: true},
		{name: "https default port", raw: "https://proxy.example", ok: true},
		{name: "trailing slash", raw: "http://10.0.0.5:3128/", ok: true},
		{name: "ipv6", raw: "socks5://[::1]:1080", ok: true},

		{name: "no scheme", raw: "127.0.0.1:1080", want: "missing the scheme"},
		{name: "socks5 without port", raw: "socks5://127.0.0.1", want: "missing the port"},
		{name: "unknown scheme", raw: "ftp://host:21", want: "unsupported scheme"},
		{name: "shadowsocks is not a proxy scheme here", raw: "ss://host:8388", want: "unsupported scheme"},
		{name: "pasted a full URL", raw: "http://host:3128/api/v1", want: "drop the path"},
		{name: "port is not a number", raw: "socks5://host:abcd", want: "invalid port"},
		{name: "no host", raw: "socks5://:1080", want: "missing the proxy host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := ParseProxy(tc.raw)
			if tc.ok {
				if err != nil {
					t.Fatalf("ParseProxy(%q) = %v, want it accepted", tc.raw, err)
				}
				if strings.TrimSpace(tc.raw) == "" && u != nil {
					t.Fatalf("empty input must mean direct, got %v", u)
				}
				return
			}
			if err == nil {
				t.Fatalf("ParseProxy(%q) was accepted, want rejected", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q — the operator has to guess", err, tc.want)
			}
		})
	}
}

// A proxy the panel cannot parse must fail every request with that reason. Falling
// back to a direct connection would be silent and wrong: the field was filled in
// precisely because direct does not work.
func TestProxyTransportRefusesRatherThanGoingDirect(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	defer srv.Close()

	client := &http.Client{Transport: ProxyTransport("ftp://nope:21"), Timeout: 5 * time.Second}
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("request succeeded — a broken proxy setting must not fall through to direct")
	}
	if reached {
		t.Fatal("the request reached the origin directly, bypassing the configured proxy")
	}
	if !strings.Contains(err.Error(), "unsupported scheme") {
		t.Errorf("error %q does not carry the reason the proxy was rejected", err)
	}
}

// The whole point of the setting: traffic must actually leave through the proxy.
// Verified with a stub CONNECT proxy rather than by inspecting the transport, so
// this fails if the wiring stops taking effect for any reason.
func TestProxyTransportRoutesThroughTheProxy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("through the proxy"))
	}))
	defer origin.Close()

	var proxied int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A plain (non-CONNECT) HTTP proxy request: the transport sends the absolute
		// URL, which is what tells us it chose the proxy over a direct dial.
		if !r.URL.IsAbs() {
			t.Errorf("got a direct-style request %q, want an absolute URL", r.URL)
		}
		proxied++
		out, err := http.Get(r.URL.String())
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer out.Body.Close()
		w.WriteHeader(out.StatusCode)
		_, _ = io.Copy(w, out.Body)
	}))
	defer proxy.Close()

	// A fresh transport each time would be cached across subtests by proxy string;
	// this one is unique to the stub's port, so it gets its own.
	client := &http.Client{Transport: ProxyTransport(proxy.URL), Timeout: 5 * time.Second}
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("request through the proxy failed: %v", err)
	}
	defer resp.Body.Close()
	if proxied != 1 {
		t.Fatalf("the proxy saw %d requests, want 1", proxied)
	}
}

// Changing the setting must take effect, not keep serving the old route from cache.
func TestProxyTransportRebuildsWhenTheProxyChanges(t *testing.T) {
	first := ProxyTransport("socks5://127.0.0.1:1080")
	if same := ProxyTransport("socks5://127.0.0.1:1080"); same != first {
		t.Fatal("an unchanged proxy rebuilt the transport — connection pools would be thrown away on every call")
	}
	if changed := ProxyTransport("socks5://127.0.0.1:9050"); changed == first {
		t.Fatal("a changed proxy kept the old transport — the new setting would never apply")
	}
}

// GetVia with no proxy must keep every guard Get has; loopback is the case that
// matters, since that is what an SSRF attempt reaches for.
func TestGetViaWithoutProxyStillGuards(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := GetVia(ctx, srv.URL, 1<<10, ""); err == nil {
		t.Fatal("a loopback URL was fetched — the SSRF guard is not running on the direct path")
	}
}

// With a proxy the destination cannot be address-checked (the proxy resolves it),
// but the URL itself is still judged: plain http and embedded credentials stay out.
func TestGetViaKeepsURLShapeChecks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, raw := range []string{
		"http://example.com/x.js",
		"https://user:pass@example.com/x.js",
		"",
	} {
		if _, err := GetVia(ctx, raw, 1<<10, "socks5://127.0.0.1:1"); err == nil {
			t.Errorf("GetVia accepted %q", raw)
		}
	}
}
