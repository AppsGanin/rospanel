package sub

import (
	"encoding/json"
	"fmt"
	"github.com/AppsGanin/rospanel/internal/extsub"
	"net/url"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The Xray JSON subscription format: one full client config per lane per server,
// as a JSON array. It is what Xray-core apps (Happ, v2rayNG, v2rayN, Streisand)
// import when a subscription hands them JSON instead of links — and the only form
// in which client-side DPI evasion (fragment, noise) reaches them, since a share
// link has no field for either.
//
// Each config is derived from the share link the panel already builds for that
// lane, so the two can never disagree about a port, an SNI or a REALITY key: the
// link is parsed back into an outbound rather than assembled a second time.

// XrayJSONMulti renders the user's lanes across every server as an array of Xray
// configs. dpi decides whether a fragment/noise outbound is chained in.
func XrayJSONMulti(u model.User, servers []Server, dpi model.SubDPI) string {
	configs := make([]map[string]any, 0, 8)
	for _, l := range ShareLinksAll(u, servers) {
		if cfg, ok := xrayConfigFromLink(l, dpi); ok {
			configs = append(configs, cfg)
		}
	}
	b, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(b)
}

// XrayJSONWithTemplate renders each lane into the operator's own Xray config instead
// of the panel's. The template is ONE config — the format is an array of independent
// configs, one per lane, and the client picks between them — so {{outbounds}} takes
// that lane's whole outbound chain (its proxy, the optional fragment/noise dialer,
// direct and block) and {{remarks}} its display name.
//
// Falls back to the generated profile on an unparseable template or a lane that will
// not render: a client that cannot parse the array drops every server in it.
func XrayJSONWithTemplate(u model.User, servers []Server, dpi model.SubDPI, template string) (string, error) {
	if strings.TrimSpace(template) == "" {
		return XrayJSONMulti(u, servers, dpi), nil
	}
	configs := make([]any, 0, 8)
	for _, l := range ShareLinksAll(u, servers) {
		cfg, ok := xrayConfigFromLink(l, dpi)
		if !ok {
			continue
		}
		// A lane with no outbound chain would render into the operator's document as an
		// empty outbounds list: a config that parses, imports, and routes nothing. Refuse
		// rather than hand that out — the caller falls back to the generated profile,
		// which is a working subscription. Defensive rather than reachable today, but
		// the cost of being wrong here is a client that looks connected and is not.
		outbounds, ok := cfg["outbounds"].([]map[string]any)
		if !ok || len(outbounds) == 0 {
			return XrayJSONMulti(u, servers, dpi), fmt.Errorf("lane %q produced no outbound chain", cfg["remarks"])
		}
		chain := make([]any, len(outbounds))
		for i, o := range outbounds {
			chain[i] = o
		}
		rendered, err := renderJSONTemplate(template,
			map[string]any{TplRemarks: cfg["remarks"]},
			map[string][]any{TplOutbounds: chain},
		)
		if err != nil {
			return XrayJSONMulti(u, servers, dpi), err
		}
		var doc any
		if err := json.Unmarshal([]byte(rendered), &doc); err != nil {
			return XrayJSONMulti(u, servers, dpi), err
		}
		configs = append(configs, doc)
	}
	if len(configs) == 0 {
		return XrayJSONMulti(u, servers, dpi), nil
	}
	b, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return XrayJSONMulti(u, servers, dpi), err
	}
	return string(b), nil
}

// xrayConfigFromLink turns one share link into a complete client config: local
// SOCKS/HTTP inbounds on the ports every Xray app expects, the proxy outbound, the
// optional fragment/noise dialer, direct and block, and a routing block that keeps
// private ranges local. Returns false for a scheme the format cannot carry.
func xrayConfigFromLink(raw string, dpi model.SubDPI) (map[string]any, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	proxy, ok := xrayOutbound(parsed)
	if !ok {
		return nil, false
	}
	outbounds := []map[string]any{proxy}
	// Fragment only where there is a ClientHello with our real SNI to hide — a
	// TLS-over-TCP lane. REALITY shows the donor's name and gains nothing from a
	// split; plain transports have no handshake. Noise has no such restriction.
	//
	// A QUIC lane is chained through neither: it never dials through the TCP
	// `freedom` outbound a dialerProxy points at, so one would do nothing at best.
	security := parsed.Query().Get("security")
	if quic := parsed.Scheme == "hysteria2"; !quic {
		if shaper := fragmentOutbound(dpi, security == "tls"); shaper != nil {
			stream, _ := proxy["streamSettings"].(map[string]any)
			stream["sockopt"] = map[string]any{"dialerProxy": "fragment", "tcpNoDelay": true}
			outbounds = append(outbounds, shaper)
		}
	}
	outbounds = append(outbounds,
		map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "UseIP"}},
		map[string]any{"tag": "block", "protocol": "blackhole", "settings": map[string]any{}},
	)
	remarks := parsed.Fragment
	if unescaped, err := url.PathUnescape(remarks); err == nil {
		remarks = unescaped
	}
	return map[string]any{
		"remarks": remarks,
		"log":     map[string]any{"loglevel": "warning"},
		"dns":     map[string]any{"servers": []any{"1.1.1.1", "8.8.8.8"}},
		"inbounds": []map[string]any{
			{
				"tag": "socks", "listen": "127.0.0.1", "port": 10808, "protocol": "socks",
				"settings": map[string]any{"auth": "noauth", "udp": true},
				"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": false},
			},
			{
				"tag": "http", "listen": "127.0.0.1", "port": 10809, "protocol": "http",
				"settings": map[string]any{"auth": "noauth"},
			},
		},
		"outbounds": outbounds,
		"routing": map[string]any{
			"domainStrategy": "IPIfNonMatch",
			// The private ranges are spelled out rather than written as
			// "geoip:private": that shorthand needs geoip.dat next to the client, and
			// a client without it refuses the WHOLE config rather than just the rule.
			// The literal list means the same thing and depends on nothing.
			"rules": []map[string]any{
				{"type": "field", "outboundTag": "direct", "ip": privateRanges()},
			},
		},
	}, true
}

// privateRanges is what "geoip:private" expands to: the loopback, link-local and
// RFC1918 blocks plus their IPv6 counterparts, kept local instead of tunnelled.
func privateRanges() []string {
	return []string{
		"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"169.254.0.0/16", "100.64.0.0/10", "224.0.0.0/4", "255.255.255.255/32",
		"::1/128", "fc00::/7", "fe80::/10",
	}
}

// fragmentOutbound is the freedom outbound the proxy dials through when fragment
// or noise is on. nil when neither applies to this lane. Both settings ride on one
// outbound: Xray applies fragment to the TLS handshake it sees and noise before
// the packets it sends, independently.
func fragmentOutbound(dpi model.SubDPI, tlsLane bool) map[string]any {
	dpi = dpi.Normalized()
	settings := map[string]any{}
	if dpi.Fragment && tlsLane {
		settings["fragment"] = map[string]any{
			"packets": dpi.FragmentPackets, "length": dpi.FragmentLength, "interval": dpi.FragmentInterval,
		}
	}
	if dpi.Noise {
		settings["noises"] = []map[string]any{{
			"type": dpi.NoiseType, "packet": dpi.NoisePacket, "delay": dpi.NoiseDelay,
		}}
	}
	if len(settings) == 0 {
		return nil
	}
	return map[string]any{
		"tag": "fragment", "protocol": "freedom", "settings": settings,
		"streamSettings": map[string]any{"sockopt": map[string]any{"tcpNoDelay": true}},
	}
}

// xrayOutbound builds the proxy outbound for a share link. The reading of the link
// lives in extsub, where the same code also dials external servers as the upstreams
// of an egress lane: one reading of a link for both consumers.
func xrayOutbound(l *url.URL) (map[string]any, bool) {
	return extsub.XrayOutbound(l.String(), "proxy")
}
