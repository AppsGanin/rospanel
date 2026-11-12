package xray

import (
	"encoding/json"
	"strings"

	"github.com/AppsGanin/rospanel/internal/tuning"
)

// tcpDefaults adds socket defaults at the supervisor boundary, so locally built
// configs and configs pushed by an older panel get the same policy. It preserves
// explicit operator settings, unknown fields, and numeric precision. Reapplying
// it leaves the original bytes intact, which matters to live user updates.
func tcpDefaults(data []byte) []byte {
	if !json.Valid(data) {
		return data
	}
	cfg, err := decodeRaw(data)
	if err != nil || cfg == nil {
		return data // The ordinary config validator reports malformed input.
	}
	changed := false
	for _, key := range []string{"inbounds", "outbounds"} {
		entries, _ := cfg[key].([]any)
		for _, entry := range entries {
			e, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			protocol, _ := e["protocol"].(string)
			switch protocol {
			case "vless", "vmess", "trojan", "shadowsocks", "socks", "http", "dokodemo-door", "tunnel", "freedom":
			default:
				continue // Includes UDP-only WireGuard/Hysteria and internal outbounds.
			}
			listen, _ := e["listen"].(string)
			if key == "inbounds" && (strings.HasPrefix(listen, "@") || strings.HasPrefix(listen, "/")) {
				continue
			}
			st, ok := e["streamSettings"].(map[string]any)
			if !ok {
				if e["streamSettings"] != nil {
					continue
				}
				st = map[string]any{}
			}
			network, _ := st["network"].(string)
			switch network {
			case "", "tcp", "raw", "ws", "xhttp", "splithttp", "grpc", "httpupgrade", "h2", "http":
			default:
				continue
			}
			so, ok := st["sockopt"].(map[string]any)
			if !ok {
				if st["sockopt"] != nil {
					continue
				}
				so = map[string]any{}
			}
			keepalive := 15
			for _, k := range []string{"tcpKeepAliveIdle", "tcpKeepAliveInterval"} {
				if n, ok := so[k].(json.Number); ok {
					if i, err := n.Int64(); err == nil && i < 0 {
						keepalive = -1 // Xray rejects a negative/positive pair.
					}
				}
			}
			defaults := map[string]any{"tcpKeepAliveIdle": keepalive, "tcpKeepAliveInterval": keepalive, "tcpMptcp": false}
			if mptcp, _ := so["tcpMptcp"].(bool); !mptcp {
				// Linux MPTCP cannot apply TCP_USER_TIMEOUT. An explicit MPTCP
				// choice stays the operator's, without an incompatible default.
				defaults["tcpUserTimeout"] = tuning.TCPUserTimeout.Milliseconds()
			}
			for k, v := range defaults {
				if _, exists := so[k]; !exists {
					so[k], changed = v, true
				}
			}
			st["sockopt"], e["streamSettings"] = so, st
		}
	}
	if !changed {
		return data
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return data
	}
	return out
}
