package sub

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/extsub"

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

	vless := singboxVLESS(nV, u.UUID, set)
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
	// Relayed external servers ride the TCP-TLS lane only: sing-box has no XHTTP for
	// the REALITY one.
	for _, r := range srv.relayEntries(u) {
		if r.lane == model.LaneVLESS {
			proxies = append(proxies, singboxVLESS(r.name, r.user.UUID, set))
			tags = append(tags, r.name)
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
	if !in.SingBoxCarries() {
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

	host, port := in.ClientEndpoint(set.Host)
	out := map[string]any{
		"type": in.Protocol, "tag": tag, "server": host, "server_port": port,
	}
	if in.Protocol == model.InbVLESS {
		out["uuid"] = u.UUID
		if o.Flow != "" {
			out["flow"] = o.Flow
		}
	} else {
		out["password"] = u.Password
	}

	switch o.ClientSecurity() {
	case model.SecTLS:
		tls := map[string]any{
			// A CDN's certificate is publicly trusted, whatever the server's own is.
			"enabled": true, "server_name": clashSNI(in, set), "insecure": set.TLSInsecure && !o.ViaCDN(),
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
	return singboxJSON(u, servers, SingBoxCaps{})
}

// singboxJSON is SingBoxJSONMulti with the NaiveProxy outbounds when the client can load
// them (SingBoxNaive).
func singboxJSON(u model.User, servers []Server, caps SingBoxCaps) string {
	if len(servers) == 0 {
		return "{}"
	}
	local := servers[0].Set

	proxies, tags := singboxProxiesAll(u, servers)
	if caps.Naive {
		naiveOut, naiveTags := singboxNaiveAll(u, servers, tags)
		proxies, tags = append(proxies, naiveOut...), append(tags, naiveTags...)
	}
	if caps.AnyTLS {
		atOut, atTags := singboxAnyTLSAll(u, servers, tags)
		proxies, tags = append(proxies, atOut...), append(tags, atTags...)
	}

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
	// the censor does on plaintext UDP/53.
	//
	// DNS servers are written in the typed form ("type" + "server") that sing-box 1.12
	// introduced. The old one-string form ("address": "https://1.1.1.1/dns-query") was
	// removed in 1.14, and a 1.14 client refuses the WHOLE profile over it — every
	// server gone, not just DNS. The typed form loads on 1.12 and later, which is the
	// floor this profile already had: tls.fragment below is 1.12 too.
	dnsServers := []any{
		map[string]any{"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": group},
	}
	dns := map[string]any{"servers": dnsServers, "final": "remote", "strategy": "prefer_ipv4"}
	route := map[string]any{"final": group, "auto_detect_interface": true}
	// A server whose host is a domain has to be resolved before the tunnel exists, so
	// it cannot go through "remote" — that detours through the very outbound it is
	// resolving, and the first connect deadlocks. It is resolved directly instead,
	// across all nodes, not just the local server.
	//
	// Which DNS server resolves an outbound's own host used to be picked by a DNS rule
	// matching the host names. Since 1.12 it is route.default_domain_resolver, and 1.14
	// refuses a profile that dials a domain without one. An all-IP profile resolves
	// nothing, so it gets neither the bootstrap server nor the resolver.
	//
	// The bootstrap is Yandex's DoH, because it has to answer from the user's network
	// with no tunnel up. It used to be Alibaba's 223.5.5.5, whose TLS handshake hangs
	// after the ClientHello from Russian networks (measured 2026-09-15): the lookup
	// timed out, and a domain-hosted profile never connected at all. A domestic
	// resolver is also the one that survives a mobile whitelist, and all it ever
	// resolves is the panel's own server names.
	for _, srv := range servers {
		if net.ParseIP(srv.Set.Host) == nil {
			dns["servers"] = append(dnsServers,
				map[string]any{"type": "https", "tag": "bootstrap", "server": "77.88.8.8"})
			route["default_domain_resolver"] = "bootstrap"
			break
		}
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
	route["rules"] = routeRules

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
		"route":     route,
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
//
// caps adds the outbounds of the lanes not every sing-box has (see SingBoxCapsFor).
func SingBoxWithTemplate(u model.User, servers []Server, template string, caps SingBoxCaps) (string, error) {
	if strings.TrimSpace(template) == "" {
		return singboxJSON(u, servers, caps), nil
	}
	if len(servers) == 0 {
		return singboxJSON(u, servers, caps), nil
	}
	// A template saved before sing-box removed a field it uses parses fine and renders
	// fine, and every client refuses it. Refused at save now, but one already stored
	// still has to be caught here — the generated profile works on every current client.
	if err := singboxLegacyErr(template); err != nil {
		return singboxJSON(u, servers, caps), err
	}
	proxies, tags := singboxProxiesAll(u, servers)
	if caps.Naive && naiveResolvable(template) {
		naiveOut, naiveTags := singboxNaiveAll(u, servers, tags)
		proxies, tags = append(proxies, naiveOut...), append(tags, naiveTags...)
	}
	if caps.AnyTLS {
		atOut, atTags := singboxAnyTLSAll(u, servers, tags)
		proxies, tags = append(proxies, atOut...), append(tags, atTags...)
	}
	// Nothing allowed: the generated profile has a direct-only answer for this, which
	// is valid and honest. A template spliced with an empty proxy list would leave a
	// selector pointing at nothing, which sing-box refuses outright.
	if len(tags) == 0 {
		return singboxJSON(u, servers, caps), nil
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
		return singboxJSON(u, servers, caps), err
	}
	return out, nil
}

// singboxNaiveAll builds every server's NaiveProxy outbound, tagged apart from taken —
// a duplicate tag makes sing-box refuse the whole profile.
func singboxNaiveAll(u model.User, servers []Server, taken []string) ([]any, []string) {
	used := make(map[string]bool, len(taken))
	for _, t := range taken {
		used[t] = true
	}
	seen := map[string]int{}
	var proxies []any
	var tags []string
	for _, srv := range servers {
		set := srv.Set
		if !link.NaiveOn(set) || !srv.allowsBuiltin(model.LaneNaive) {
			continue
		}
		tag := uniqueLabel(seen, link.LabelFor(model.ProtoNaive, u, set))
		for used[tag] {
			tag = uniqueLabel(seen, link.LabelFor(model.ProtoNaive, u, set))
		}
		used[tag] = true
		proxies = append(proxies, map[string]any{
			"type": "naive", "tag": tag, "server": set.Host, "server_port": set.VLESSPort,
			"username": model.UserEmail(u.ID), "password": u.UUID,
			"tls": map[string]any{"enabled": true, "server_name": link.NaiveServerName(set)},
			// UDP rides the tunnel too (internal/naive, uot.go); without it the outbound
			// refuses UDP, and calls, games and QUIC behind it fail.
			"udp_over_tcp": true,
		})
		tags = append(tags, tag)
	}
	return proxies, tags
}

// naiveResolvable reports whether a template leaves sing-box a way to resolve a naive
// outbound's server. The naive outbound always looks its server up as a domain, IP or
// not, and with two or more DNS servers and no route.default_domain_resolver sing-box
// 1.13+ refuses the whole profile over it ("missing domain resolver for domain server
// address") — where every other outbound only draws a warning. Such a template gets no
// naive outbound; the generated profile always has one server or the resolver.
func naiveResolvable(template string) bool {
	var doc struct {
		DNS struct {
			Servers []any `json:"servers"`
		} `json:"dns"`
		Route struct {
			DefaultDomainResolver any `json:"default_domain_resolver"`
		} `json:"route"`
	}
	if json.Unmarshal([]byte(template), &doc) != nil {
		return false
	}
	return len(doc.DNS.Servers) < 2 || doc.Route.DefaultDomainResolver != nil
}

// SingBoxNaive reports whether a sing-box client can load a naive outbound: the
// official apps (SFA on Android, SFI/SFM/SFT on Apple — the platforms whose builds
// carry cronet) from sing-box 1.13, and husi from 0.15.3 (its core has naive from
// 0.15.2, udp_over_tcp from 0.15.3). Anything else would refuse the whole profile over
// the unknown outbound type, so it gets none.
func SingBoxNaive(userAgent string) bool {
	if m := husiUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 0, 15, 3)
	}
	m := singboxAppUA.FindStringSubmatch(userAgent)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 1 || major == 1 && minor >= 13
}

// husiUA matches husi ("husi/0.15.5 (sing-box 1.13.4)"), by its own version.
var husiUA = regexp.MustCompile(`(?i)^husi/v?(\d+)\.(\d+)\.(\d+)`)

// singboxAppUA matches the official apps' User-Agent, "SFA (sing-box 1.13.0; language
// en_US)" — the app name, then the core version.
var singboxAppUA = regexp.MustCompile(`^SF[AIMT] \(sing-box (\d+)\.(\d+)`)

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

// singboxVLESS is the TCP-TLS lane (VLESS-Vision) as a sing-box outbound, for uuid.
func singboxVLESS(tag, uuid string, set *model.Settings) map[string]any {
	vless := map[string]any{
		"type": "vless", "tag": tag, "server": set.Host, "server_port": set.VLESSPort,
		"uuid": uuid, "flow": "xtls-rprx-vision",
		"tls": map[string]any{
			"enabled": true, "server_name": set.SNI, "insecure": set.TLSInsecure,
			"utls": map[string]any{"enabled": true, "fingerprint": set.VLESSFP()},
		},
	}
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
	return vless
}

// SingBoxCaps are the lanes a sing-box client is known to load beyond the ones every
// sing-box has: a profile carries a lane only for a client that will not refuse it.
type SingBoxCaps struct {
	Naive  bool // NaiveProxy (SingBoxNaive)
	AnyTLS bool // AnyTLS (SingBoxAnyTLS)
}

// SingBoxCapsFor is what the client that asked can load.
func SingBoxCapsFor(userAgent string) SingBoxCaps {
	return SingBoxCaps{Naive: SingBoxNaive(userAgent), AnyTLS: SingBoxAnyTLS(userAgent)}
}

// SingBoxAnyTLS reports whether a sing-box client has the anytls outbound: the official
// apps from sing-box 1.12, husi from 0.10.9.
func SingBoxAnyTLS(userAgent string) bool {
	if m := husiUA.FindStringSubmatch(userAgent); m != nil {
		return versionAtLeast(m[1:], 0, 10, 9)
	}
	m := singboxAppUA.FindStringSubmatch(userAgent)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 1 || major == 1 && minor >= 12
}

// singboxAnyTLSAll builds every server's AnyTLS outbound, tagged apart from taken.
func singboxAnyTLSAll(u model.User, servers []Server, taken []string) ([]any, []string) {
	used := make(map[string]bool, len(taken))
	for _, t := range taken {
		used[t] = true
	}
	seen := map[string]int{}
	var proxies []any
	var tags []string
	for _, srv := range servers {
		set := srv.Set
		if !link.AnyTLSOn(set) || !srv.allowsBuiltin(model.LaneAnyTLS) {
			continue
		}
		tag := uniqueLabel(seen, link.LabelFor(model.ProtoAnyTLS, u, set))
		for used[tag] {
			tag = uniqueLabel(seen, link.LabelFor(model.ProtoAnyTLS, u, set))
		}
		used[tag] = true
		proxies = append(proxies, map[string]any{
			"type": "anytls", "tag": tag, "server": set.Host, "server_port": set.VLESSPort,
			"password": u.UUID,
			"tls": map[string]any{
				"enabled": true, "server_name": set.SNI,
				"utls": map[string]any{"enabled": true, "fingerprint": set.VLESSFP()},
			},
		})
		tags = append(tags, tag)
	}
	return proxies, tags
}
