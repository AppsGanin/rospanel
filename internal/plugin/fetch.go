package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const (
	maxFetchBody     = 1 << 20
	maxFetchResponse = 4 << 20
	maxFetchTimeout  = 30 * time.Second
	maxRedirects     = 5
	maxBlobTransfer  = 5 * time.Minute
)

// HTTPFetcher is panel.http.fetch. A plugin reaches only the hosts its manifest
// names, and never an address inside the box or its network — whatever a name
// resolves to. The check runs on the address actually dialled (Dialer.Control),
// so DNS rebinding and redirects to internal addresses are covered by the same
// rule. Without it, a plugin that may call "api.example.com" could still be pointed
// at the node agent, Xray's API or the panel itself on 127.0.0.1.
type HTTPFetcher struct {
	client *http.Client
	// denied decides which addresses are refused; tests swap it to reach httptest.
	denied func(netip.Addr) bool
}

// NewFetcher returns the fetcher plugins use.
func NewFetcher() *HTTPFetcher {
	f := &HTTPFetcher{denied: deniedAddr}
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("plugin fetch: unexpected address %q", address)
			}
			if f.denied(ap.Addr().Unmap()) {
				return fmt.Errorf("plugin fetch: %s is a private or local address", ap.Addr())
			}
			return nil
		},
	}
	f.client = &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil, // never an environment proxy: the address check must see the target
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: maxFetchTimeout,
			MaxIdleConns:          20,
			IdleConnTimeout:       60 * time.Second,
		},
		Timeout: maxFetchTimeout,
	}
	return f
}

// deniedAddr is every address a plugin may not reach.
func deniedAddr(a netip.Addr) bool {
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsUnspecified() ||
		a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() {
		return true
	}
	for _, p := range deniedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

var deniedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT: a provider's internal network
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking, used by some VPN/TUN setups
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 — maps onto IPv4, private included
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("2002::/16"),      // 6to4 — embeds an IPv4 address
}

// allowed reports whether a URL's host is on the allowlist: an exact name (any
// port unless the entry names one) or "*.domain" for its subdomains.
func allowed(u *url.URL, allow []string) bool {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	for _, entry := range allow {
		name, entryPort, hasPort := strings.Cut(entry, ":")
		if hasPort && entryPort != port {
			continue
		}
		if wild, ok := strings.CutPrefix(name, "*."); ok {
			if strings.HasSuffix(host, "."+wild) {
				return true
			}
			continue
		}
		if host == name {
			return true
		}
	}
	return false
}

// Fetch performs one request for a plugin with the given allowlist.
func (f *HTTPFetcher) Fetch(ctx context.Context, allow []string, req FetchRequest) (*FetchResponse, error) {
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("fetch: %q is not an http(s) URL", req.URL)
	}
	if !allowed(u, allow) {
		return nil, fmt.Errorf("fetch: %s is not in the plugin's net allowlist", u.Hostname())
	}
	if len(req.Body) > maxFetchBody {
		return nil, fmt.Errorf("fetch: body over %d KB — send a blob", maxFetchBody>>10)
	}
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	if req.BodyReader != nil {
		body = req.BodyReader
	}
	hr, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	if req.BodyReader != nil {
		hr.ContentLength = req.BodySize
		if req.BodySize < 0 {
			hr.ContentLength = -1 // chunked
		}
	}
	for k, v := range req.Headers {
		hr.Header.Set(k, v)
	}
	if hr.Header.Get("User-Agent") == "" {
		hr.Header.Set("User-Agent", "RosPanel-Plugin/1")
	}

	client := *f.client
	if req.BodyReader != nil || req.Sink != nil {
		client.Timeout = maxBlobTransfer // a blob moves more than a JSON answer; the call's deadline still bounds it
	}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("fetch: too many redirects")
		}
		if !allowed(next.URL, allow) {
			return fmt.Errorf("fetch: redirect to %s, not in the allowlist", next.URL.Hostname())
		}
		return nil
	}
	resp, err := client.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	limit := maxFetchResponse
	if req.MaxBytes > 0 {
		limit = req.MaxBytes
	}
	if req.Sink != nil {
		n, err := io.Copy(req.Sink, io.LimitReader(resp.Body, int64(limit)+1))
		if err != nil {
			return nil, fmt.Errorf("fetch: reading the answer: %w", err)
		}
		if n > int64(limit) {
			return nil, fmt.Errorf("fetch: answer over %d MB", limit>>20)
		}
		out := &FetchResponse{Status: resp.StatusCode, Headers: map[string]string{}}
		for k, v := range resp.Header {
			out.Headers[strings.ToLower(k)] = strings.Join(v, ", ")
		}
		return out, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("fetch: reading the answer: %w", err)
	}
	if len(b) > limit {
		return nil, fmt.Errorf("fetch: answer over %d MB", limit>>20)
	}
	out := &FetchResponse{Status: resp.StatusCode, Headers: map[string]string{}, Body: string(b)}
	for k, v := range resp.Header {
		out.Headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return out, nil
}

// Allowed checks a URL against an allowlist the way Fetch does, without fetching —
// for the author tools, whose mocked answers must obey the same list.
func Allowed(raw string, allow []string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("fetch: %q is not an http(s) URL", raw)
	}
	if !allowed(u, allow) {
		return fmt.Errorf("fetch: %s is not in the plugin's net allowlist", u.Hostname())
	}
	return nil
}
