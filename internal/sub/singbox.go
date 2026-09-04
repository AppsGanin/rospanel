package sub

import (
	"encoding/json"
	"fmt"
	"github.com/AppsGanin/rospanel/internal/extsub"
	"net"
	"strings"

	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// SingBoxJSON renders an importable sing-box configuration for a single server.
func SingBoxJSON(u model.User, set *model.Settings) string {
	return SingBoxJSONMulti(u, One(set))
}

// singboxProxies builds the protocol outbounds + their tags for one server. Tags
// carry the node label (see Settings.ProtoLabel) so multi-node configs stay
// unambiguous.
func singboxProxies(u model.User, srv Server) (proxies []any, tags []string) {
	set := srv.Set
	nV := link.LabelFor(model.ProtoVLESS, u, set)
	nH := link.LabelFor(model.ProtoHysteria, u, set)
	insecure := set.TLSInsecure // true only for a self-signed/IP cert

	vless := map[string]any{
		"type": "vless", "tag": nV, "server": set.Host, "server_port": set.VLESSPort,
		"uuid": u.UUID, "flow": "xtls-rprx-vision",
		"tls": map[string]any{
			"enabled": true, "server_name": set.SNI, "insecure": insecure,
			"utls": map[string]any{"enabled": true, "fingerprint": set.VLESSFP()},
		},
	}
	hy2 := map[string]any{
		"type": "hysteria2", "tag": nH, "server": set.Host, "server_port": set.HysteriaPort,
		"password": u.Password,
		"tls": map[string]any{
			"enabled": true, "server_name": set.SNI, "alpn": []string{"h3"}, "insecure": insecure,
		},
	}
	if set.HopEnd > set.HysteriaPort {
		// Port hopping: a range replaces the single server_port.
		hy2["server_ports"] = []string{fmt.Sprintf("%d:%d", model.HopAdvertised(set.HysteriaPort, set.HopStart), set.HopEnd)}
		hy2["hop_interval"] = "10s"
		delete(hy2, "server_port")
	}
	singboxObfs(hy2, set.HysteriaObfs)

	// Anti-DPI shaping of the generated config (client-side only; no server change).
	// ClientHello fragmentation (sing-box ≥1.12) defeats stateless SNI inspection on
	// the one lane whose handshake carries our real SNI — VLESS-Vision. REALITY hides
	// its SNI behind the donor and Hysteria2 is QUIC, so neither is fragmented here.
	// Fragmenting sits below the TLS record layer, so it doesn't disturb Vision's flow.
	if set.TLSFragment {
		vless["tls"].(map[string]any)["fragment"] = true
		// Record-level split on top (sing-box ≥1.12): survives a middlebox that
		// reassembles TCP segments before it looks at the ClientHello.
		if set.SubDPI.RecordFragment {
			vless["tls"].(map[string]any)["record_fragment"] = true
		}
	}
	// ALPN consistency on the Vision lane: the :443 inbound offers [h2,http/1.1];
	// offering the same aligns the ClientHello with a real browser to that cert.
	vless["tls"].(map[string]any)["alpn"] = []string{"h2", "http/1.1"}

	// Only the lanes enabled in the Connections panel become outbounds; tags collects
	// them in the same order for the selector/urltest groups.
	//
	// The built-in REALITY lane is deliberately absent: it runs on XHTTP, for which
	// sing-box has no transport at all. Xray-core clients still get it through the
	// universal link list — see model.SupportsSingBox, which applies the same rule to
	// custom inbounds.
	if set.VLESSEnabled && srv.allowsBuiltin(model.LaneVLESS) {
		proxies = append(proxies, vless)
		tags = append(tags, nV)
	}
	if set.HysteriaEnabled && srv.allowsBuiltin(model.LaneHysteria) {
		proxies = append(proxies, hy2)
		tags = append(tags, nH)
	}
	for _, in := range srv.Custom {
		if !srv.allowsInbound(in.ID) {
			continue
		}
		if o, tag, ok := singboxCustom(u, in, set); ok {
			proxies = append(proxies, o)
			tags = append(tags, tag)
		}
	}
	for _, e := range srv.externalEndpoints() {
		if o, ok := extsub.SingBoxOutbound(e, e.Name); ok {
			proxies = append(proxies, o)
			tags = append(tags, e.Name)
		}
	}
	return proxies, tags
}

// singboxCustom renders one custom inbound as a sing-box outbound, or reports false
// when sing-box cannot express that protocol × transport (see model.SupportsSingBox
// — most notably it has no XHTTP at all). Dropped rather than approximated, for the
// same reason as the Clash side.
func singboxCustom(u model.User, in model.Inbound, set *model.Settings) (map[string]any, string, bool) {
	if !model.SupportsSingBox(in.Protocol, in.Opts.Transport) {
		return nil, "", false
	}
	o := in.Opts
	tag := link.CustomLabelFor(in, u, set)

	if in.Protocol == model.InbHysteria {
		out := map[string]any{
			"type": "hysteria2", "tag": tag, "server": set.Host, "server_port": in.Port,
			"password": u.Password,
			"tls": map[string]any{
				"enabled": true, "server_name": clashSNI(in, set),
				"alpn": []string{"h3"}, "insecure": set.TLSInsecure,
			},
		}
		if in.UsesHopping() {
			out["server_ports"] = []string{fmt.Sprintf("%d:%d", model.HopAdvertised(in.Port, o.HopStart), o.HopEnd)}
			out["hop_interval"] = "10s"
			delete(out, "server_port")
		}
		singboxObfs(out, o.Obfs)
		return out, tag, true
	}

	if in.Protocol == model.InbShadowsocks {
		// sing-box's shadowsocks: method plus the server-key:user-key password. It
		// relays UDP by default, so there is no udp flag to set as there is on Clash.
		return map[string]any{
			"type": "shadowsocks", "tag": tag, "server": set.Host, "server_port": in.Port,
			"method":   o.Method,
			"password": o.ShadowKey + ":" + model.UserShadowKey(u.UUID, o.Method),
		}, tag, true
	}

	out := map[string]any{
		"type": in.Protocol, "tag": tag, "server": set.Host, "server_port": in.Port,
	}
	if in.Protocol == model.InbVLESS {
		out["uuid"] = u.UUID
		if o.Flow != "" {
			out["flow"] = o.Flow
		}
	} else {
		out["password"] = u.Password
	}

	switch o.Security {
	case model.SecTLS:
		tls := map[string]any{
			"enabled": true, "server_name": clashSNI(in, set), "insecure": set.TLSInsecure,
			"utls": map[string]any{"enabled": true, "fingerprint": o.FPOr()},
		}
		if o.Transport == model.TrGRPC {
			tls["alpn"] = []string{"h2"}
		}
		out["tls"] = tls
	case model.SecReality:
		out["tls"] = map[string]any{
			"enabled": true, "server_name": o.RealitySNI(),
			"utls":    map[string]any{"enabled": true, "fingerprint": o.FPOr()},
			"reality": map[string]any{"enabled": true, "public_key": o.RealityPublicKey, "short_id": firstShortID(o)},
		}
	}

	switch o.Transport {
	case model.TrWS:
		out["transport"] = map[string]any{
			"type": "ws", "path": o.Path,
			"headers": map[string]any{"Host": clashHost(in, set)},
		}
	case model.TrHTTPUpgrade:
		out["transport"] = map[string]any{
			"type": "httpupgrade", "path": o.Path, "host": clashHost(in, set),
		}
	case model.TrGRPC:
		out["transport"] = map[string]any{"type": "grpc", "service_name": o.ServiceName}
	}
	return out, tag, true
}

// SingBoxJSONMulti renders a sing-box config spanning every server (local + each
// node): one outbound per lane × server, all gathered under the selector/urltest
// groups. servers[0] is the local server, used for the group title + DNS bootstrap
// anchor.
func SingBoxJSONMulti(u model.User, servers []Server) string {
	if len(servers) == 0 {
		return "{}"
	}
	local := servers[0].Set

	proxies, tags := singboxProxiesAll(u, servers)

	group := SubTitle(u, local)
	// Nothing allowed ⇒ no tags. A urltest with an empty outbound list and a selector
	// pointing at it are load errors in sing-box, so the client would refuse the entire
	// profile instead of showing an account with no servers. Answer a direct-only
	// config: valid, honest, and it starts working the moment access is granted.
	if len(tags) == 0 {
		out, err := json.MarshalIndent(map[string]any{
			"log":       map[string]any{"level": "warn"},
			"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
			"route":     map[string]any{"final": "direct"},
		}, "", "  ")
		if err != nil {
			return "{}"
		}
		return string(out)
	}
	outbounds := []any{
		map[string]any{"type": "selector", "tag": group, "outbounds": append([]string{"auto"}, tags...), "default": "auto"},
		map[string]any{"type": "urltest", "tag": "auto", "outbounds": tags,
			"url": "https://www.gstatic.com/generate_204", "interval": "5m"},
	}
	outbounds = append(outbounds, proxies...)
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})

	// Encrypted DNS (DoH) routed through the tunnel — defeats DNS poisoning/blocking
	// the censor does on plaintext UDP/53. Every server host that is a domain is
	// resolved directly (bootstrap) so the first tunnel connect doesn't deadlock on
	// DNS — across all nodes, not just the local server.
	dnsServers := []any{
		map[string]any{"tag": "remote", "address": "https://1.1.1.1/dns-query", "detour": group},
	}
	dns := map[string]any{"servers": dnsServers, "final": "remote", "strategy": "prefer_ipv4"}
	var bootstrapHosts []string
	for _, srv := range servers {
		if net.ParseIP(srv.Set.Host) == nil {
			bootstrapHosts = append(bootstrapHosts, srv.Set.Host)
		}
	}
	if len(bootstrapHosts) > 0 {
		dns["servers"] = append(dnsServers,
			map[string]any{"tag": "bootstrap", "address": "https://223.5.5.5/dns-query", "detour": "direct"})
		dns["rules"] = []any{map[string]any{"domain": bootstrapHosts, "server": "bootstrap"}}
	}

	routeRules := []any{
		map[string]any{"action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
	}
	if local.BlockQUIC {
		// Drop untunneled browser QUIC (UDP/443) so it can't slip past the obfuscated
		// TCP lanes under the censor's QUIC classifiers — the browser falls back to
		// TCP+H2 inside the tunnel.
		routeRules = append(routeRules, map[string]any{"network": "udp", "port": 443, "action": "reject"})
	}
	routeRules = append(routeRules, map[string]any{"ip_is_private": true, "outbound": "direct"})

	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		"dns": dns,
		"inbounds": []any{
			map[string]any{
				"type": "tun", "tag": "tun-in",
				"address":      []string{"172.19.0.1/30"},
				"auto_route":   true,
				"strict_route": true,
				"stack":        "system",
			},
		},
		"outbounds": outbounds,
		"route": map[string]any{
			"rules":                 routeRules,
			"final":                 group,
			"auto_detect_interface": true,
		},
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

// SingBoxWithTemplate renders the user's outbounds into the operator's own sing-box
// document. {{proxies}} takes the generated outbounds, {{tags}} their tags and
// {{group}} the profile's name — enough to write any group layout, DNS and rule set
// on top of servers the panel still owns.
//
// Falls back to the generated profile whenever the template cannot produce a working
// one: unparseable, or the user has no servers to put in it. A client that cannot
// parse a profile drops all of it, so serving the plain one is always better than
// serving a broken document.
func SingBoxWithTemplate(u model.User, servers []Server, template string) (string, error) {
	if strings.TrimSpace(template) == "" {
		return SingBoxJSONMulti(u, servers), nil
	}
	if len(servers) == 0 {
		return SingBoxJSONMulti(u, servers), nil
	}
	proxies, tags := singboxProxiesAll(u, servers)
	// Nothing allowed: the generated profile has a direct-only answer for this, which
	// is valid and honest. A template spliced with an empty proxy list would leave a
	// selector pointing at nothing, which sing-box refuses outright.
	if len(tags) == 0 {
		return SingBoxJSONMulti(u, servers), nil
	}
	tagList := make([]any, len(tags))
	for i, t := range tags {
		tagList[i] = t
	}
	out, err := renderJSONTemplate(template,
		map[string]any{TplGroup: SubTitle(u, servers[0].Set)},
		map[string][]any{TplProxies: proxies, TplTags: tagList},
	)
	if err != nil {
		return SingBoxJSONMulti(u, servers), err
	}
	return out, nil
}

// singboxProxiesAll gathers every server's outbounds, giving each a tag no other
// outbound shares. A duplicate tag is fatal in sing-box — the selector would name it
// twice and the profile is refused — so the de-duplication is not cosmetic; see
// uniqueLabel for how two differently-named lanes end up asking for the same tag.
func singboxProxiesAll(u model.User, servers []Server) ([]any, []string) {
	var proxies []any
	var tags []string
	seen := map[string]int{}
	for _, srv := range servers {
		p, t := singboxProxies(u, srv)
		for i := range p {
			uniq := uniqueLabel(seen, t[i])
			if uniq != t[i] {
				if m, ok := p[i].(map[string]any); ok {
					m["tag"] = uniq
				}
				t[i] = uniq
			}
			proxies = append(proxies, p[i])
			tags = append(tags, t[i])
		}
	}
	return proxies, tags
}

// singboxObfs adds sing-box's Salamander block to a Hysteria2 outbound, or leaves
// it alone when the lane is not obfuscated. sing-box's implementation is wire
// compatible with the Xray finalmask mask the server runs, so one key serves both.
func singboxObfs(out map[string]any, obfs string) {
	if obfs == "" {
		return
	}
	out["obfs"] = map[string]any{"type": "salamander", "password": obfs}
}
