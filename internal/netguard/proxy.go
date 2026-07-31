package netguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// This file is the outbound-proxy half of the package: an operator-configured proxy
// for reaching a host the server otherwise cannot.
//
// It lives next to the SSRF guards rather than in internal/telegram (its only
// caller today) for two reasons: core needs the same parser to validate the setting
// and cannot import telegram — that dependency is deliberately one-way — and
// building outbound HTTP clients is already this package's job.
//
// Note the different threat models. The guards in fetch.go exist because a fetch
// TARGET can be attacker-influenced, so it is resolved and checked against private
// ranges. A proxy address is not: it is typed by an authenticated admin, and the
// likeliest value is a loopback or LAN address — a SOCKS inbound on the same box.
// So a proxy is never subjected to rejectPrivateIP; doing so would reject the
// normal case.

// ProxySchemes are the proxy URL schemes supported. All four are handled by
// net/http itself; nothing here implements a proxy protocol.
//
// socks5 and socks5h are both accepted and behave identically: Go's SOCKS5 dialer
// always sends the hostname for the proxy to resolve, which is what the "h" asks
// for. Both are listed because operators paste whichever their proxy documented.
var ProxySchemes = []string{"http", "https", "socks5", "socks5h"}

// ParseProxy validates an operator-supplied proxy URL. An empty string means
// "direct" and yields (nil, nil) — callers treat a nil URL as no proxy.
//
// Errors are plain English; callers wrap them into localised validation messages.
func ParseProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	// Checked before parsing, because a bare "host:port" does not survive url.Parse
	// in any useful shape: "127.0.0.1:1080" fails outright ("first path segment in
	// URL cannot contain colon") while "proxy.example:1080" parses with the hostname
	// as the SCHEME. Both would produce a message about something the operator did
	// not write. Pasting host:port is the common slip, so name it.
	if !strings.Contains(raw, "://") {
		return nil, fmt.Errorf("missing the scheme — write it as socks5://%s", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Host == "" {
		return nil, errors.New("missing the proxy host")
	}
	if !slices.Contains(ProxySchemes, u.Scheme) {
		return nil, fmt.Errorf("unsupported scheme %q — use one of: %s",
			u.Scheme, strings.Join(ProxySchemes, ", "))
	}
	// A proxy is addressed by host and port; a path means something else was pasted
	// (an API endpoint, a subscription link). Accepting it would silently ignore the
	// part the operator thought mattered.
	if p := strings.Trim(u.Path, "/"); p != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("a proxy address is host and port only — drop the path")
	}
	host, port, err := net.SplitHostPort(u.Host)
	switch {
	case err != nil:
		// http/https have well-known default ports and net/http fills them in; SOCKS
		// has none, so a missing port there fails at dial time with an error that
		// names neither the setting nor the panel.
		if u.Scheme == "http" || u.Scheme == "https" {
			if strings.TrimSpace(u.Hostname()) == "" {
				return nil, errors.New("missing the proxy host")
			}
			return u, nil
		}
		return nil, fmt.Errorf("missing the port — write it as %s://%s:1080", u.Scheme, u.Host)
	case strings.TrimSpace(host) == "":
		return nil, errors.New("missing the proxy host")
	case port == "":
		return nil, fmt.Errorf("missing the port — write it as %s://%s:1080", u.Scheme, host)
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return nil, fmt.Errorf("invalid port %q", port)
	}
	return u, nil
}

// proxyFunc builds the Transport.Proxy hook for a configured proxy.
//
// A malformed value fails every request with that parse error rather than quietly
// going direct. Silently ignoring it would be the worst outcome available: the
// operator set the field precisely because direct does not work, so "direct" is
// both wrong and indistinguishable from the problem they were fixing. Saving is
// validated too — this covers a row that predates the validation or was edited
// outside the panel.
func proxyFunc(raw string) func(*http.Request) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	u, err := ParseProxy(raw)
	if err != nil {
		return func(*http.Request) (*url.URL, error) {
			return nil, fmt.Errorf("the configured proxy is invalid: %w", err)
		}
	}
	return http.ProxyURL(u)
}

// One shared transport, rebuilt only when the proxy setting changes.
//
// It has to be shared: callers construct a client per notification and per test
// send, and a fresh Transport each time would leave a connection pool behind on
// every call. Only one entry is kept because this is a single panel-wide setting —
// at most one proxy is in use at a time.
var (
	transportMu  sync.Mutex
	transportRaw string
	transport    *http.Transport
)

// ProxyTransport returns the shared transport for the given proxy URL (empty =
// direct). Unlike Client, it applies no SSRF validation to the destination: with a
// proxy in the path the name is resolved at the far end, so there is no address
// here to check. Use it for a fixed, code-controlled destination — never for a URL
// that arrived in a request.
func ProxyTransport(raw string) *http.Transport {
	transportMu.Lock()
	defer transportMu.Unlock()
	if transport != nil && transportRaw == raw {
		return transport
	}
	if transport != nil {
		// The proxy changed: don't leave idle connections parked against an address
		// nothing will use again. In-flight requests are untouched and finish on the
		// old transport.
		transport.CloseIdleConnections()
	}
	transportRaw, transport = raw, &http.Transport{
		Proxy: proxyFunc(raw),
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return transport
}

// GetVia is Get, optionally routed through an operator-configured proxy.
//
// With no proxy it IS Get, guards and all. With one, the destination host is no
// longer resolved locally — the proxy does that — so rejectPrivateHost cannot run,
// and does not: the URL is still required to be https without embedded
// credentials, but the address checks are structurally unavailable. That is
// acceptable only because both inputs are trusted here: rawURL is a constant in our
// own code and the proxy comes from an authenticated admin. Do not hand this a URL
// that came from a request.
func GetVia(ctx context.Context, rawURL string, maxBody int64, proxy string) ([]byte, error) {
	if strings.TrimSpace(proxy) == "" {
		return Get(ctx, rawURL, maxBody)
	}
	if err := validateFetchURLShape(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: defaultFetchTimeout, Transport: ProxyTransport(proxy)}
	if deadline, ok := ctx.Deadline(); ok {
		client.Timeout = max(time.Until(deadline), time.Millisecond)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}
