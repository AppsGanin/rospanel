package sub

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/msTimofeev/rospanel/internal/link"
	"github.com/msTimofeev/rospanel/internal/model"
)

// SingBoxJSON renders an importable sing-box configuration (current 1.11+ schema)
// with a tun inbound, the enabled protocol outbounds, an auto urltest + manual
// selector, and a minimal route. Targeted at official SFA/SFI/SFM clients; other
// sing-box-based clients (Hiddify, NekoBox) consume the base64 list instead.
func SingBoxJSON(u model.User, set *model.Settings) string {
	nV := link.Label(model.ProtoVLESS, u, set)
	nR := link.Label(model.ProtoReality, u, set)
	nT := link.Label(model.ProtoTrojan, u, set)
	nH := link.Label(model.ProtoHysteria, u, set)
	insecure := set.TLSInsecure // true only for a self-signed/IP cert

	vless := map[string]any{
		"type": "vless", "tag": nV, "server": set.Host, "server_port": set.VLESSPort,
		"uuid": u.UUID, "flow": "xtls-rprx-vision",
		"tls": map[string]any{
			"enabled": true, "server_name": set.SNI, "insecure": insecure,
			"utls": map[string]any{"enabled": true, "fingerprint": set.VLESSFP()},
		},
	}
	// VLESS + gRPC + REALITY: borrows RealityDest's TLS (no insecure flag), grpc
	// transport, no Vision flow.
	reality := map[string]any{
		"type": "vless", "tag": nR, "server": set.Host, "server_port": set.RealityPort,
		"uuid": u.UUID,
		"tls": map[string]any{
			"enabled": true, "server_name": set.RealitySNI(),
			"utls":    map[string]any{"enabled": true, "fingerprint": set.RealityFP()},
			"reality": map[string]any{"enabled": true, "public_key": set.RealityPublicKey, "short_id": set.RealitySID()},
		},
		"transport": map[string]any{"type": "grpc", "service_name": set.RealityServiceName},
	}
	trojan := map[string]any{
		"type": "trojan", "tag": nT, "server": set.Host, "server_port": set.VLESSPort,
		"password": u.Password,
		"tls":      map[string]any{"enabled": true, "server_name": set.SNI, "insecure": insecure},
		"transport": map[string]any{
			"type": "ws", "path": set.WSPath,
			"headers": map[string]any{"Host": set.SNI},
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
		hy2["server_ports"] = []string{fmt.Sprintf("%d:%d", set.HysteriaPort, set.HopEnd)}
		hy2["hop_interval"] = "10s"
		delete(hy2, "server_port")
	}

	// Anti-DPI shaping of the generated config (client-side only; no server change).
	// ClientHello fragmentation (sing-box ≥1.12) defeats stateless SNI inspection on
	// the lanes whose handshake carries our real SNI — VLESS-Vision and Trojan-WS.
	// REALITY already hides its SNI behind the donor and Hysteria2 is QUIC, so
	// neither is fragmented here. Fragmenting sits below the TLS record layer, so it
	// doesn't disturb Vision's flow or the Trojan WebSocket upgrade.
	if set.TLSFragment {
		vless["tls"].(map[string]any)["fragment"] = true
		trojan["tls"].(map[string]any)["fragment"] = true
	}
	// ALPN consistency on the Vision lane: the :443 inbound offers [h2,http/1.1];
	// offering the same aligns the ClientHello with a real browser to that cert.
	// (Deliberately NOT on Trojan-WS — WebSocket needs http/1.1 and the shared :443
	// fallback dispatches it, so forcing h2 there could break the upgrade.)
	vless["tls"].(map[string]any)["alpn"] = []string{"h2", "http/1.1"}
	// Match the VLESS uTLS fingerprint on Trojan too (they share the :443 host+cert),
	// so a passive classifier doesn't see a Go-stdlib ClientHello beside a browser one.
	trojan["tls"].(map[string]any)["utls"] = map[string]any{"enabled": true, "fingerprint": set.TrojanFP()}

	// Only the protocols enabled in the Connections panel become outbounds; tags
	// list collects them in the same order for the selector/urltest groups.
	var proxies []any
	var tags []string
	if set.VLESSEnabled {
		proxies = append(proxies, vless)
		tags = append(tags, nV)
	}
	if set.RealityEnabled {
		proxies = append(proxies, reality)
		tags = append(tags, nR)
	}
	if set.TrojanEnabled {
		proxies = append(proxies, trojan)
		tags = append(tags, nT)
	}
	if set.HysteriaEnabled {
		proxies = append(proxies, hy2)
		tags = append(tags, nH)
	}

	group := SubTitle(set)
	outbounds := []any{
		map[string]any{"type": "selector", "tag": group, "outbounds": append([]string{"auto"}, tags...), "default": "auto"},
		map[string]any{"type": "urltest", "tag": "auto", "outbounds": tags,
			"url": "https://www.gstatic.com/generate_204", "interval": "5m"},
	}
	outbounds = append(outbounds, proxies...)
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})

	// Encrypted DNS (DoH) routed through the tunnel — defeats DNS poisoning/blocking
	// the censor does on plaintext UDP/53. A domain server-host is resolved directly
	// (bootstrap) so the first tunnel connect doesn't deadlock on DNS.
	dnsServers := []any{
		map[string]any{"tag": "remote", "address": "https://1.1.1.1/dns-query", "detour": group},
	}
	dns := map[string]any{"servers": dnsServers, "final": "remote", "strategy": "prefer_ipv4"}
	if net.ParseIP(set.Host) == nil {
		dns["servers"] = append(dnsServers,
			map[string]any{"tag": "bootstrap", "address": "https://223.5.5.5/dns-query", "detour": "direct"})
		dns["rules"] = []any{map[string]any{"domain": []string{set.Host}, "server": "bootstrap"}}
	}

	routeRules := []any{
		map[string]any{"action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
	}
	if set.BlockQUIC {
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
