package sub

import (
	"fmt"
	"strings"

	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// SubTitle is the per-user profile title: the configured subscription title (or
// "RosPanel" by default), optionally suffixed with the user name when
// SubNameInTitle is enabled.
func SubTitle(u model.User, set *model.Settings) string {
	base := strings.TrimSpace(set.SubTitle)
	if base == "" {
		// One source of truth for the stock name — it used to be duplicated here as
		// a literal, so rebranding the panel left the client profile title behind.
		base = branding.DefaultName
	}
	if set.SubNameInTitle {
		if name := strings.TrimSpace(u.Name); name != "" {
			return base + " — " + name
		}
	}
	return base
}

// clashProxy is one Clash proxy: its node name and the YAML flow-map line
// (already indented with "  - ").
type clashProxy struct {
	name string
	line string
}

// clashProxies builds the enabled-lane Clash proxy entries for a user on one server.
//
// Every stream-based lane carries `udp: true`. Mihomo defaults a proxy's UDP support
// to FALSE, and when a rule resolves to a proxy that can't take UDP it SKIPS that
// rule and keeps matching — the packet falls through to DIRECT instead of the tunnel.
// That is what broke Telegram calls on mihomo clients (Koala Clash, FlClashX): the
// voice UDP left untunneled and the censor dropped it. Xray-core clients have no such
// flag and were never affected, and Hysteria2 escaped it only because mihomo hardcodes
// UDP support for that protocol — hence "calls work only on the Hysteria lane".
func clashProxies(u model.User, srv Server) []clashProxy {
	set := srv.Set
	sv := "false" // skip-cert-verify: true only for a self-signed/IP cert
	if set.TLSInsecure {
		sv = "true"
	}
	var out []clashProxy
	if set.VLESSEnabled && srv.allowsBuiltin(model.LaneVLESS) {
		n := link.Label(model.ProtoVLESS, set)
		out = append(out, clashProxy{n, fmt.Sprintf(
			"  - {name: %q, type: vless, server: %q, port: %d, uuid: %q, network: tcp, tls: true, udp: true, servername: %q, flow: xtls-rprx-vision, client-fingerprint: %s, skip-cert-verify: %s}",
			n, set.Host, set.VLESSPort, u.UUID, set.SNI, set.VLESSFP(), sv)})
	}
	if set.RealityEnabled && srv.allowsBuiltin(model.LaneReality) {
		n := link.Label(model.ProtoReality, set)
		out = append(out, clashProxy{n, fmt.Sprintf(
			"  - {name: %q, type: vless, server: %q, port: %d, uuid: %q, network: xhttp, tls: true, udp: true, servername: %q, client-fingerprint: %s, reality-opts: {public-key: %q, short-id: %q}, xhttp-opts: {path: %q}}",
			n, set.Host, set.RealityPort, u.UUID, set.RealitySNI(), set.RealityFP(), set.RealityPublicKey, set.RealitySID(), set.RealityPathOr())})
	}
	if set.HysteriaEnabled && srv.allowsBuiltin(model.LaneHysteria) {
		hop := ""
		if set.HopEnd > set.HysteriaPort {
			hop = fmt.Sprintf(", ports: %q", fmt.Sprintf("%d-%d", model.HopAdvertised(set.HysteriaPort, set.HopStart), set.HopEnd))
		}
		n := link.Label(model.ProtoHysteria, set)
		out = append(out, clashProxy{n, fmt.Sprintf(
			"  - {name: %q, type: hysteria2, server: %q, port: %d, password: %q, sni: %q, alpn: [h3], skip-cert-verify: %s%s}",
			n, set.Host, set.HysteriaPort, u.Password, set.SNI, sv, hop)})
	}
	for _, in := range srv.Custom {
		if !srv.allowsInbound(in.ID) {
			continue
		}
		if p, ok := clashCustom(u, in, set, sv); ok {
			out = append(out, p)
		}
	}
	return out
}

// clashCustom renders one custom inbound as a Clash proxy, or reports false when
// mihomo cannot express that protocol × transport (see model.SupportsClash). An
// inexpressible combination is DROPPED rather than approximated: a client that
// rejects one proxy entry usually rejects the whole profile, so a bad line would
// cost the user every other server too.
func clashCustom(u model.User, in model.Inbound, set *model.Settings, sv string) (clashProxy, bool) {
	if !model.SupportsClash(in.Protocol, in.Opts.Transport) {
		return clashProxy{}, false
	}
	o := in.Opts
	n := link.CustomLabel(in, set)

	if in.Protocol == model.InbHysteria {
		hop := ""
		if in.UsesHopping() {
			hop = fmt.Sprintf(", ports: %q", fmt.Sprintf("%d-%d", model.HopAdvertised(in.Port, o.HopStart), o.HopEnd))
		}
		return clashProxy{n, fmt.Sprintf(
			"  - {name: %q, type: hysteria2, server: %q, port: %d, password: %q, sni: %q, alpn: [h3], skip-cert-verify: %s%s}",
			n, set.Host, in.Port, u.Password, clashSNI(in, set), sv, hop)}, true
	}

	if in.Protocol == model.InbShadowsocks {
		// mihomo's Shadowsocks-2022 shape: cipher is the method, and the password is
		// the server key and the user key joined by a colon (the multi-user form).
		pw := o.ShadowKey + ":" + model.UserShadowKey(u.UUID, o.Method)
		return clashProxy{n, fmt.Sprintf(
			"  - {name: %q, type: ss, server: %q, port: %d, cipher: %s, password: %q, udp: true}",
			n, set.Host, in.Port, o.Method, pw)}, true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  - {name: %q, type: %s, server: %q, port: %d",
		n, in.Protocol, set.Host, in.Port)
	if in.Protocol == model.InbVLESS {
		fmt.Fprintf(&b, ", uuid: %q", u.UUID)
		if o.Flow != "" {
			fmt.Fprintf(&b, ", flow: %s", o.Flow)
		}
	} else {
		fmt.Fprintf(&b, ", password: %q", u.Password)
	}
	fmt.Fprintf(&b, ", network: %s, udp: true", o.Transport)

	switch o.Security {
	case model.SecTLS:
		// Trojan spells the server name "sni", VLESS spells it "servername".
		key := "servername"
		if in.Protocol == model.InbTrojan {
			key = "sni"
		}
		fmt.Fprintf(&b, ", tls: true, %s: %q, client-fingerprint: %s, skip-cert-verify: %s",
			key, clashSNI(in, set), o.FPOr(), sv)
	case model.SecReality:
		fmt.Fprintf(&b, ", tls: true, servername: %q, client-fingerprint: %s, reality-opts: {public-key: %q, short-id: %q}",
			o.RealitySNI(), o.FPOr(), o.RealityPublicKey, firstShortID(o))
	}

	switch o.Transport {
	case model.TrWS:
		fmt.Fprintf(&b, ", ws-opts: {path: %q, headers: {Host: %q}}", o.Path, clashHost(in, set))
	case model.TrGRPC:
		fmt.Fprintf(&b, ", grpc-opts: {grpc-service-name: %q}", o.ServiceName)
	case model.TrXHTTP:
		fmt.Fprintf(&b, ", xhttp-opts: {path: %q", o.Path)
		if o.Host != "" {
			fmt.Fprintf(&b, ", host: %q", o.Host)
		}
		if o.Mode != "" {
			fmt.Fprintf(&b, ", mode: %q", o.Mode)
		}
		b.WriteString("}")
	}
	b.WriteString("}")
	return clashProxy{n, b.String()}, true
}

// clashSNI is the server name a custom inbound's client should present.
func clashSNI(in model.Inbound, set *model.Settings) string {
	if in.Opts.SNI != "" {
		return in.Opts.SNI
	}
	return set.SNI
}

// clashHost is the Host header for the HTTP-shaped transports (defaults to the SNI).
func clashHost(in model.Inbound, set *model.Settings) string {
	if in.Opts.Host != "" {
		return in.Opts.Host
	}
	return clashSNI(in, set)
}

// firstShortID is the REALITY shortId that goes into client configs (the server
// accepts any of the stored set).
func firstShortID(o model.InboundOpts) string {
	if ids := o.RealityShortIDs(); len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// clashProxiesAll concatenates a user's proxy entries across every server (local +
// each node). Names are unique because Settings.ProtoLabel appends the node label.
func clashProxiesAll(u model.User, servers []Server) []clashProxy {
	var out []clashProxy
	for _, srv := range servers {
		out = append(out, clashProxies(u, srv)...)
	}
	return out
}

// ClashYAML renders a minimal self-contained Clash-Meta config for one server.
func ClashYAML(u model.User, set *model.Settings) string {
	return ClashYAMLMulti(u, One(set))
}

// ClashYAMLMulti renders a Clash-Meta (Mihomo) config spanning every server: all
// proxies under one select group. servers[0] is the local server (group title + rules).
func ClashYAMLMulti(u model.User, servers []Server) string {
	if len(servers) == 0 {
		return ""
	}
	local := servers[0].Set
	proxies := clashProxiesAll(u, servers)
	var b strings.Builder
	// Encrypted DNS (DoH) to defeat DNS poisoning/blocking on plaintext UDP/53.
	b.WriteString("dns:\n" +
		"  enable: true\n" +
		"  enhanced-mode: fake-ip\n" +
		"  nameserver: [\"https://1.1.1.1/dns-query\", \"https://dns.google/dns-query\"]\n")
	b.WriteString("proxies:\n")
	quoted := make([]string, len(proxies))
	for i, p := range proxies {
		b.WriteString(p.line + "\n")
		quoted[i] = fmt.Sprintf("%q", p.name)
	}
	group := clashGroupName(u, local)
	// A user whose groups grant nothing has no proxies at all. A select group with no
	// members is a LOAD ERROR in mihomo, so emitting one makes the client reject the
	// whole document — the account looks broken rather than empty. Reachable without
	// misconfiguration: any membership row restricts the user, so a group that grants
	// nothing yet, or whose only grants were a deleted node's lanes, lands here.
	if len(proxies) == 0 {
		b.WriteString("rules:\n  - \"MATCH,DIRECT\"\n")
		return b.String()
	}
	fmt.Fprintf(&b,
		"proxy-groups:\n  - {name: %q, type: select, proxies: [%s]}\n",
		group, strings.Join(quoted, ", "))
	b.WriteString("rules:\n")
	if local.BlockQUIC {
		// Drop untunneled browser QUIC (UDP/443) so it can't bypass the obfuscated
		// TCP lanes; the browser falls back to TCP+H2 inside the tunnel.
		b.WriteString("  - AND,((NETWORK,udp),(DST-PORT,443)),REJECT\n")
	}
	// The WHOLE rule is one quoted YAML scalar, never `MATCH,%q`: a rule is a plain
	// string that mihomo splits on commas itself, so quoting only the group name left
	// the quotes IN the target and the profile was rejected outright with
	// `rules[N] [MATCH,"..."] error: proxy ["..."] not found`. Quoting the whole line
	// also keeps a title containing ": " from turning the list item into a YAML map.
	fmt.Fprintf(&b, "  - %q\n", "MATCH,"+group)
	return b.String()
}

// clashGroupName is the select-group name for the generated profile. Mihomo parses a
// rule line by splitting on commas, so a comma in the operator's subscription title
// (or the user name appended to it) would be read as a rule separator and shift the
// MATCH target — strip it here, where both the group definition and the rule read the
// same name.
func clashGroupName(u model.User, set *model.Settings) string {
	return strings.TrimSpace(strings.ReplaceAll(SubTitle(u, set), ",", " "))
}

// ClashWithTemplateMulti injects the user's proxies into a RoscomVPN-style Mihomo
// routing template. The template carries two "# LEAVE THIS LINE!" markers: the
// `proxies:` line (full proxy definitions) and a slot inside the main select group
// (proxy node names). Falls back to the plain config if the template has no proxies
// marker.
func ClashWithTemplateMulti(u model.User, servers []Server, template string) string {
	proxies := clashProxiesAll(u, servers)
	if len(proxies) == 0 || !strings.Contains(template, "proxies: # LEAVE THIS LINE!") {
		return ClashYAMLMulti(u, servers)
	}

	defs := make([]string, len(proxies))
	for i, p := range proxies {
		defs[i] = p.line
	}
	out := strings.Replace(template,
		"proxies: # LEAVE THIS LINE!",
		"proxies:\n"+strings.Join(defs, "\n"),
		1,
	)

	// Add the proxy node names to the main select group (6-space list items).
	var names strings.Builder
	for _, p := range proxies {
		fmt.Fprintf(&names, "      - %q\n", p.name)
	}
	out = strings.Replace(out, "    # LEAVE THIS LINE!", strings.TrimRight(names.String(), "\n"), 1)
	return out
}
