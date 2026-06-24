// Package proxypool fetches and parses outbound proxy lists for the proxy-pool
// egress: free public lists or a manually-entered set, one
// "scheme://[user:pass@]host:port" per line.
package proxypool

import (
	"bufio"
	"bytes"
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/netguard"
)

// Parse turns proxy lines into endpoints, skipping blanks/comments/dupes and
// unsupported schemes (socks4 — Xray has no socks4 outbound).
func Parse(lines []string) []model.ProxyEndpoint {
	seen := make(map[string]struct{})
	var out []model.ProxyEndpoint
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		if !strings.Contains(ln, "://") {
			ln = "socks5://" + ln // bare host:port ⇒ assume socks5
		}
		u, err := url.Parse(ln)
		if err != nil || u.Hostname() == "" || u.Port() == "" {
			continue
		}
		var proto string
		switch strings.ToLower(u.Scheme) {
		case "socks", "socks5", "socks5h":
			proto = "socks"
		case "http", "https":
			proto = "http"
		default:
			continue // socks4 etc. are not supported as Xray outbounds
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		key := proto + "://" + u.Host
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		ep := model.ProxyEndpoint{Protocol: proto, Address: u.Hostname(), Port: port}
		if u.User != nil {
			ep.User = u.User.Username()
			ep.Pass, _ = u.User.Password()
		}
		out = append(out, ep)
	}
	return out
}

// Fetch downloads a proxy-list URL and returns its non-empty lines. The URL is
// SSRF-validated (https only, no private/metadata addresses) before any request.
func Fetch(ctx context.Context, rawURL string) ([]string, error) {
	if err := netguard.ValidateFetchURL(rawURL); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	body, err := netguard.Get(ctx, rawURL, 1<<20)
	if err != nil {
		return nil, err
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}
