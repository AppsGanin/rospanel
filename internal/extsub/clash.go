package extsub

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// The subscription formats that are not a list of links need each external server
// rewritten in their own vocabulary. Both converters here follow the rule the
// panel's own lanes follow (model.SupportsClash / SupportsSingBox): a combination
// the client cannot express is DROPPED rather than approximated, because an app
// that rejects one entry usually rejects the whole profile.

// ClashProxy renders an endpoint as one Clash Meta (mihomo) proxy line, flow-style
// YAML, with the name the entry goes by. sv is the profile's skip-cert-verify
// value; it is never raised for a foreign server (a link carries no proof its
// certificate is self-signed, and "trust anything" is the wrong default).
func ClashProxy(ep Endpoint) (name, line string, ok bool) {
	name = ep.Name
	switch ep.Protocol {
	case "vmess":
		return name, clashVMess(ep, name), true
	case "ss":
		l, err := url.Parse(ep.Link)
		if err != nil || l.User == nil {
			return "", "", false
		}
		method, password, ok := ShadowsocksUserinfo(l.User.Username())
		if !ok {
			return "", "", false
		}
		return name, fmt.Sprintf("  - {name: %q, type: ss, server: %q, port: %d, cipher: %q, password: %q, udp: true}",
			name, ep.Host, ep.Port, method, password), true
	}
	l, err := url.Parse(ep.Link)
	if err != nil || l.User == nil {
		return "", "", false
	}
	q := l.Query()
	cred := l.User.Username()
	switch ep.Protocol {
	case "hysteria2":
		password, err := url.QueryUnescape(cred)
		if err != nil {
			password = cred
		}
		ports := ""
		if mport := q.Get("mport"); mport != "" {
			ports = fmt.Sprintf(", ports: %q", mport)
		}
		return name, fmt.Sprintf("  - {name: %q, type: hysteria2, server: %q, port: %d, password: %q, sni: %q, alpn: [h3], skip-cert-verify: false%s}",
			name, ep.Host, ep.Port, password, q.Get("sni"), ports), true
	case "vless", "trojan":
		network, transport, ok := clashTransport(ep.Protocol, firstNonEmpty(q.Get("type"), "tcp"), q)
		if !ok {
			return "", "", false
		}
		security := q.Get("security")
		if ep.Protocol == "trojan" && security != "tls" {
			return "", "", false // mihomo's trojan is TLS by definition
		}
		var b strings.Builder
		fmt.Fprintf(&b, "  - {name: %q, type: %s, server: %q, port: %d, ", name, ep.Protocol, ep.Host, ep.Port)
		if ep.Protocol == "vless" {
			fmt.Fprintf(&b, "uuid: %q, ", cred)
			if flow := q.Get("flow"); flow != "" {
				fmt.Fprintf(&b, "flow: %s, ", flow)
			}
		} else {
			fmt.Fprintf(&b, "password: %q, ", cred)
		}
		fmt.Fprintf(&b, "network: %s, udp: true", network)
		switch security {
		case "tls":
			fmt.Fprintf(&b, ", tls: true, servername: %q, skip-cert-verify: false", q.Get("sni"))
			if fp := q.Get("fp"); fp != "" {
				fmt.Fprintf(&b, ", client-fingerprint: %s", fp)
			}
			if alpn := q.Get("alpn"); alpn != "" {
				fmt.Fprintf(&b, ", alpn: [%s]", alpn)
			}
		case "reality":
			if ep.Protocol != "vless" {
				return "", "", false
			}
			fmt.Fprintf(&b, ", tls: true, servername: %q, client-fingerprint: %s, reality-opts: {public-key: %q, short-id: %q}",
				q.Get("sni"), firstNonEmpty(q.Get("fp"), "chrome"), q.Get("pbk"), q.Get("sid"))
		}
		b.WriteString(transport)
		b.WriteString("}")
		return name, b.String(), true
	}
	return "", "", false
}

// clashTransport is the network as mihomo names it and the per-transport options
// fragment; false for a transport mihomo has no words for — the HTTP-header
// masquerade, and XHTTP on anything but VLESS.
func clashTransport(protocol, network string, q url.Values) (string, string, bool) {
	switch network {
	case "tcp":
		if q.Get("headerType") == "http" {
			return "", "", false // the HTTP-header masquerade has no mihomo form
		}
		return "tcp", "", true
	case "ws":
		return "ws", fmt.Sprintf(", ws-opts: {path: %q, headers: {Host: %q}}", firstNonEmpty(q.Get("path"), "/"), q.Get("host")), true
	case "httpupgrade":
		// mihomo's HTTPUpgrade is WebSocket with a flag. Named once: a second
		// "network" key in the entry makes mihomo refuse the whole profile.
		return "ws", fmt.Sprintf(", ws-opts: {path: %q, headers: {Host: %q}, v2ray-http-upgrade: true}",
			firstNonEmpty(q.Get("path"), "/"), q.Get("host")), true
	case "grpc":
		return "grpc", fmt.Sprintf(", grpc-opts: {grpc-service-name: %q}", q.Get("serviceName")), true
	case "xhttp":
		if protocol != "vless" {
			return "", "", false // mihomo's XHTTP is VLESS's alone
		}
		opts, ok := clashXHTTP(q)
		return "xhttp", opts, ok
	}
	return "", "", false
}

// clashXHTTP is the xhttp-opts fragment of a VLESS XHTTP link. The link's extra
// settings are mapped the way mihomo maps them when it reads the same link itself
// (parseXHTTPExtra in its common/convert/v.go), so a server handed out through the
// profile works as it would added by hand. False for a mode mihomo does not know.
func clashXHTTP(q url.Values) (string, bool) {
	opts := yamlPairs{{"path", firstNonEmpty(q.Get("path"), "/")}}
	if host := q.Get("host"); host != "" {
		opts.set("host", host)
	}
	switch mode := q.Get("mode"); mode {
	case "":
	case "auto", "stream-one", "stream-up", "packet-up":
		opts.set("mode", mode)
	default:
		return "", false
	}
	var extra map[string]any
	if raw := q.Get("extra"); raw != "" && json.Unmarshal([]byte(raw), &extra) == nil {
		xhttpExtra(extra, &opts)
	}
	return ", xhttp-opts: " + flowYAML(opts), true
}

// xhttpExtra adds to opts what mihomo takes from an Xray XHTTP "extra" object.
func xhttpExtra(extra map[string]any, opts *yamlPairs) {
	str := func(src, dst string) {
		if v, ok := extra[src].(string); ok && v != "" {
			opts.set(dst, v)
		}
	}
	num := func(src, dst string) {
		if v, ok := extra[src].(float64); ok {
			opts.set(dst, int(v))
		}
	}
	if v, ok := extra["noGRPCHeader"].(bool); ok && v {
		opts.set("no-grpc-header", true)
	}
	str("xPaddingBytes", "x-padding-bytes")
	if v, ok := extra["xPaddingObfsMode"].(bool); ok {
		opts.set("x-padding-obfs-mode", v)
	}
	str("xPaddingKey", "x-padding-key")
	str("xPaddingHeader", "x-padding-header")
	str("xPaddingPlacement", "x-padding-placement")
	str("xPaddingMethod", "x-padding-method")
	str("uplinkHTTPMethod", "uplink-http-method")
	if _, ok := extra["sessionIDPlacement"].(string); ok {
		str("sessionIDPlacement", "session-placement")
	} else {
		str("sessionPlacement", "session-placement")
	}
	if _, ok := extra["sessionIDKey"].(string); ok {
		str("sessionIDKey", "session-key")
	} else {
		str("sessionKey", "session-key")
	}
	str("sessionIDTable", "session-table")
	if v, ok := extra["sessionIDLength"].(float64); ok {
		opts.set("session-length", strconv.FormatInt(int64(v), 10))
	} else {
		str("sessionIDLength", "session-length")
	}
	str("seqPlacement", "seq-placement")
	str("seqKey", "seq-key")
	str("uplinkDataPlacement", "uplink-data-placement")
	str("uplinkDataKey", "uplink-data-key")
	num("uplinkChunkSize", "uplink-chunk-size")
	num("scMaxEachPostBytes", "sc-max-each-post-bytes")
	num("scMinPostsIntervalMs", "sc-min-posts-interval-ms")
	if xmux, ok := extra["xmux"].(map[string]any); ok {
		if reuse := xmuxReuse(xmux); len(reuse) > 0 {
			opts.set("reuse-settings", reuse)
		}
	}
	if ds, ok := extra["downloadSettings"].(map[string]any); ok {
		if d := xhttpDownload(ds); len(d) > 0 {
			opts.set("download-settings", d)
		}
	}
}

// xmuxReuse is Xray's xmux as mihomo's reuse-settings.
func xmuxReuse(xmux map[string]any) yamlPairs {
	var reuse yamlPairs
	for _, k := range [][2]string{
		{"maxConnections", "max-connections"},
		{"maxConcurrency", "max-concurrency"},
		{"cMaxReuseTimes", "c-max-reuse-times"},
		{"hMaxRequestTimes", "h-max-request-times"},
		{"hMaxReusableSecs", "h-max-reusable-secs"},
	} {
		switch v := xmux[k[0]].(type) {
		case string:
			if v != "" {
				reuse.set(k[1], v)
			}
		case float64:
			reuse.set(k[1], strconv.FormatInt(int64(v), 10))
		}
	}
	if v, ok := xmux["hKeepAlivePeriod"].(float64); ok {
		reuse.set("h-keep-alive-period", int(v))
	}
	return reuse
}

// xhttpDownload is Xray's downloadSettings as mihomo's download-settings.
func xhttpDownload(ds map[string]any) yamlPairs {
	var d yamlPairs
	if v, ok := ds["address"].(string); ok && v != "" {
		d.set("server", v)
	}
	if v, ok := ds["port"].(float64); ok {
		d.set("port", int(v))
	}
	security, _ := ds["security"].(string)
	security = strings.ToLower(security)
	if security == "tls" || security == "reality" {
		d.set("tls", true)
		if tls, ok := ds["tlsSettings"].(map[string]any); ok {
			if v, ok := tls["serverName"].(string); ok && v != "" {
				d.set("servername", v)
			}
			if v, ok := tls["fingerprint"].(string); ok && v != "" {
				d.set("client-fingerprint", v)
			}
			var alpn []string
			if list, ok := tls["alpn"].([]any); ok {
				for _, a := range list {
					if s, ok := a.(string); ok {
						alpn = append(alpn, s)
					}
				}
			}
			if len(alpn) > 0 {
				d.set("alpn", alpn)
			}
			if v, ok := tls["allowInsecure"].(bool); ok && v {
				d.set("skip-cert-verify", true)
			}
		}
		if reality, ok := ds["realitySettings"].(map[string]any); security == "reality" && ok {
			var r yamlPairs
			if v, ok := reality["publicKey"].(string); ok && v != "" {
				r.set("public-key", v)
			}
			if v, ok := reality["shortId"].(string); ok && v != "" {
				r.set("short-id", v)
			}
			if len(r) > 0 {
				d.set("reality-opts", r)
			}
		}
	}
	if x, ok := ds["xhttpSettings"].(map[string]any); ok {
		if v, ok := x["path"].(string); ok && v != "" {
			d.set("path", v)
		}
		if v, ok := x["host"].(string); ok && v != "" {
			d.set("host", v)
		}
		if v, ok := x["headers"].(map[string]any); ok && len(v) > 0 {
			d.set("headers", v)
		}
		if extra, ok := x["extra"].(map[string]any); ok {
			if xmux, ok := extra["xmux"].(map[string]any); ok {
				if reuse := xmuxReuse(xmux); len(reuse) > 0 {
					d.set("reuse-settings", reuse)
				}
			}
		}
	}
	return d
}

// yamlPairs is a flow-style YAML mapping that keeps the order it was built in.
type yamlPairs []yamlPair

type yamlPair struct {
	key string
	val any
}

func (p *yamlPairs) set(key string, val any) { *p = append(*p, yamlPair{key, val}) }

// flowYAML renders v as flow-style YAML. Every string is quoted, keys taken from a
// link too: the link is someone else's, and an unquoted value could end the entry
// early and cost the user the whole profile.
func flowYAML(v any) string {
	switch x := v.(type) {
	case yamlPairs:
		parts := make([]string, len(x))
		for i, kv := range x {
			parts[i] = kv.key + ": " + flowYAML(kv.val)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[string]any:
		parts := make([]string, 0, len(x))
		for _, k := range slices.Sorted(maps.Keys(x)) {
			parts = append(parts, strconv.Quote(k)+": "+flowYAML(x[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []string:
		parts := make([]string, len(x))
		for i, s := range x {
			parts[i] = strconv.Quote(s)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		return strconv.Quote(x)
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return `""`
}

func clashVMess(ep Endpoint, name string) string {
	cfg, _ := vmessConfig(ep.Link)
	var b strings.Builder
	fmt.Fprintf(&b, "  - {name: %q, type: vmess, server: %q, port: %d, uuid: %q, alterId: %d, cipher: %s, udp: true",
		name, ep.Host, ep.Port, cfg.ID, anyInt(cfg.AID), firstNonEmpty(cfg.SCY, "auto"))
	q := url.Values{}
	q.Set("path", cfg.Path)
	q.Set("host", cfg.Host)
	q.Set("serviceName", cfg.Path)
	q.Set("headerType", cfg.Type)
	if network, transport, ok := clashTransport("vmess", firstNonEmpty(cfg.Net, "tcp"), q); ok {
		fmt.Fprintf(&b, ", network: %s%s", network, transport)
	}
	if strings.EqualFold(cfg.TLS, "tls") {
		fmt.Fprintf(&b, ", tls: true, servername: %q, skip-cert-verify: false", firstNonEmpty(cfg.SNI, cfg.Host))
		if cfg.FP != "" {
			fmt.Fprintf(&b, ", client-fingerprint: %s", cfg.FP)
		}
	}
	b.WriteString("}")
	return b.String()
}
