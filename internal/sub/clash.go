package sub

import (
	"fmt"
	"strings"

	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// SubTitle is the per-user profile title: the configured subscription title (or
// «РосПанель» by default), optionally suffixed with the user name when
// SubNameInTitle is enabled.
func SubTitle(u model.User, set *model.Settings) string {
	base := strings.TrimSpace(set.SubTitle)
	if base == "" {
		base = "РосПанель"
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

// clashProxies builds the enabled-protocol Clash proxy entries for a user.
func clashProxies(u model.User, set *model.Settings) []clashProxy {
	sv := "false" // skip-cert-verify: true only for a self-signed/IP cert
	if set.TLSInsecure {
		sv = "true"
	}
	var out []clashProxy
	if set.VLESSEnabled {
		n := link.Label(model.ProtoVLESS)
		out = append(out, clashProxy{n, fmt.Sprintf(
			"  - {name: %q, type: vless, server: %q, port: %d, uuid: %q, network: tcp, tls: true, servername: %q, flow: xtls-rprx-vision, client-fingerprint: %s, skip-cert-verify: %s}",
			n, set.Host, set.VLESSPort, u.UUID, set.SNI, set.VLESSFP(), sv)})
	}
	if set.RealityEnabled {
		n := link.Label(model.ProtoReality)
		out = append(out, clashProxy{n, fmt.Sprintf(
			"  - {name: %q, type: vless, server: %q, port: %d, uuid: %q, network: grpc, tls: true, servername: %q, client-fingerprint: %s, reality-opts: {public-key: %q, short-id: %q}, grpc-opts: {grpc-service-name: %q}}",
			n, set.Host, set.RealityPort, u.UUID, set.RealitySNI(), set.RealityFP(), set.RealityPublicKey, set.RealitySID(), set.RealityServiceName)})
	}
	if set.TrojanEnabled {
		n := link.Label(model.ProtoTrojan)
		out = append(out, clashProxy{n, fmt.Sprintf(
			"  - {name: %q, type: trojan, server: %q, port: %d, password: %q, network: ws, sni: %q, client-fingerprint: %s, skip-cert-verify: %s, ws-opts: {path: %q, headers: {Host: %q}}}",
			n, set.Host, set.VLESSPort, u.Password, set.SNI, set.TrojanFP(), sv, set.WSPath, set.SNI)})
	}
	if set.HysteriaEnabled {
		hop := ""
		if set.HopEnd > set.HysteriaPort {
			hop = fmt.Sprintf(", ports: %q", fmt.Sprintf("%d-%d", set.HysteriaPort, set.HopEnd))
		}
		n := link.Label(model.ProtoHysteria)
		out = append(out, clashProxy{n, fmt.Sprintf(
			"  - {name: %q, type: hysteria2, server: %q, port: %d, password: %q, sni: %q, alpn: [h3], skip-cert-verify: %s%s}",
			n, set.Host, set.HysteriaPort, u.Password, set.SNI, sv, hop)})
	}
	return out
}

// ClashYAML renders a minimal self-contained Clash-Meta (Mihomo) configuration:
// the user's proxies plus a single select group and a catch-all rule.
func ClashYAML(u model.User, set *model.Settings) string {
	proxies := clashProxies(u, set)
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
	group := SubTitle(u, set)
	fmt.Fprintf(&b,
		"proxy-groups:\n  - {name: %q, type: select, proxies: [%s]}\n",
		group, strings.Join(quoted, ", "))
	b.WriteString("rules:\n")
	if set.BlockQUIC {
		// Drop untunneled browser QUIC (UDP/443) so it can't bypass the obfuscated
		// TCP lanes; the browser falls back to TCP+H2 inside the tunnel.
		b.WriteString("  - AND,((NETWORK,udp),(DST-PORT,443)),REJECT\n")
	}
	fmt.Fprintf(&b, "  - MATCH,%q\n", group)
	return b.String()
}

// ClashWithTemplate injects the user's proxies into a RoscomVPN-style Mihomo
// routing template. The template carries two "# LEAVE THIS LINE!" markers: the
// `proxies:` line (full proxy definitions) and a slot inside the main select
// group (proxy node names). Falls back to the plain config if the template has
// no proxies marker.
func ClashWithTemplate(u model.User, set *model.Settings, template string) string {
	proxies := clashProxies(u, set)
	if len(proxies) == 0 || !strings.Contains(template, "proxies: # LEAVE THIS LINE!") {
		return ClashYAML(u, set)
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
