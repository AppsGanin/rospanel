// Package proxypool fetches and parses the upstreams of an egress lane: a proxy
// list (one "scheme://[user:pass@]host:port" per line) or a subscription of share
// links (vless://, trojan://, ss://, vmess://, hysteria2://) — plain, base64 or a
// happ://crypt… link, exactly as the apps trade them. A lane can therefore leave
// through somebody else's VPN server as easily as through a socks proxy.
package proxypool

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/extsub"
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
		// A share link is an upstream too: kept whole, since the outbound is built
		// from it, and identified the way a subscription identifies it.
		if ep, ok := extsub.Parse(ln); ok {
			key := "link:" + ep.Key()
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			// The label (#fragment) is dropped: the outbound never reads it, and the
			// refresh compares endpoints to decide whether Xray must restart — a
			// provider renaming a server is not a reason to cut every session.
			link, _, _ := strings.Cut(ep.Link, "#")
			out = append(out, model.ProxyEndpoint{Protocol: ep.Protocol, Address: ep.Host, Port: ep.Port, Link: link})
			continue
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

// Fetch downloads a proxy-list or subscription URL and returns its lines, decoded
// when the body is a base64 blob or a happ://crypt… link. The URL is
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
	body, err := netguard.Get(ctx, rawURL, 2<<20)
	if err != nil {
		return nil, err
	}
	return extsub.Decode(body), nil
}
