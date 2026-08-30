package happ

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Shu1t3/rospanel-shu1t3/internal/xray"
)

// ToXrayOutbound converts a Happ Node into an xray.Outbound config object.
// Returns an error if the URI cannot be parsed or the protocol is unsupported.
func ToXrayOutbound(n *Node) (*xray.Outbound, error) {
	switch n.Protocol {
	case "vless":
		return vlessOutbound(n)
	case "vmess":
		return vmessOutbound(n)
	case "trojan":
		return trojanOutbound(n)
	case "ss":
		return ssOutbound(n)
	case "hysteria2", "hysteria":
		return hysteria2Outbound(n)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", n.Protocol)
	}
}

// ── VLESS outbound ────────────────────────────────────────────────────────

type vlessOutboundSettings struct {
	VNEXT []vlessServer `json:"vnext"`
}

type vlessServer struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []vlessUser `json:"users"`
}

type vlessUser struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
	Flow       string `json:"flow,omitempty"`
}

func vlessOutbound(n *Node) (*xray.Outbound, error) {
	u, err := url.Parse(n.URI)
	if err != nil || u.User == nil {
		return nil, fmt.Errorf("vless: invalid URI: %w", err)
	}
	q := u.Query()
	ss, err := parseStreamSettings(q, u.Host)
	if err != nil {
		return nil, fmt.Errorf("vless: %w", err)
	}
	return &xray.Outbound{
		Tag:      n.XrayTag(),
		Protocol: "vless",
		Settings: vlessOutboundSettings{
			VNEXT: []vlessServer{{
				Address: n.Host,
				Port:    n.Port,
				Users: []vlessUser{{
					ID:         u.User.Username(),
					Encryption: "none",
					Flow:       q.Get("flow"),
				}},
			}},
		},
		StreamSettings: ss,
	}, nil
}

// ── VMess outbound ────────────────────────────────────────────────────────

type vmessOutboundSettings struct {
	VNEXT []vmessServer `json:"vnext"`
}

type vmessServer struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []vmessUser `json:"users"`
}

type vmessUser struct {
	ID       string `json:"id"`
	AlterId  int    `json:"alterId"`
	Security string `json:"security"`
}

type vmessFullConfig struct {
	Add  string `json:"add"`
	Port any    `json:"port"`
	ID   string `json:"id"`
	Aid  any    `json:"aid"`
	Net  string `json:"net"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
	Path string `json:"path"`
	Host string `json:"host"`
	Scy  string `json:"scy"`
}

func vmessOutbound(n *Node) (*xray.Outbound, error) {
	b64 := strings.TrimPrefix(n.URI, "vmess://")
	b64 = strings.TrimRight(b64, "=")
	for len(b64)%4 != 0 {
		b64 += "="
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("vmess: base64: %w", err)
		}
	}
	var cfg vmessFullConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("vmess: JSON: %w", err)
	}

	port := anyToInt(cfg.Port)
	aid := anyToInt(cfg.Aid)
	security := cfg.Scy
	if security == "" {
		security = "auto"
	}

	var ss *xray.StreamSettings
	if cfg.Net != "" || cfg.TLS != "" {
		ss = &xray.StreamSettings{}
		ss.Network = cfg.Net
		if cfg.TLS == "tls" || cfg.TLS == "1" {
			ss.Security = "tls"
			ss.TLSSettings = &xray.TLSSettings{ServerName: cfg.SNI}
		}
		switch cfg.Net {
		case "ws":
			ss.WSSettings = &xray.WSSettings{
				Path:    cfg.Path,
				Headers: headerIfNonEmpty("Host", cfg.Host),
			}
		case "grpc":
			ss.GRPCSettings = &xray.GRPCSettings{ServiceName: cfg.Path}
		}
	}

	return &xray.Outbound{
		Tag:      n.XrayTag(),
		Protocol: "vmess",
		Settings: vmessOutboundSettings{
			VNEXT: []vmessServer{{
				Address: cfg.Add,
				Port:    port,
				Users: []vmessUser{{
					ID:       cfg.ID,
					AlterId:  aid,
					Security: security,
				}},
			}},
		},
		StreamSettings: ss,
	}, nil
}

// ── Trojan outbound ───────────────────────────────────────────────────────

type trojanOutboundSettings struct {
	Servers []trojanServer `json:"servers"`
}

type trojanServer struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Password string `json:"password"`
}

func trojanOutbound(n *Node) (*xray.Outbound, error) {
	u, err := url.Parse(n.URI)
	if err != nil || u.User == nil {
		return nil, fmt.Errorf("trojan: invalid URI: %w", err)
	}
	q := u.Query()
	ss, err := parseStreamSettings(q, u.Host)
	if err != nil {
		return nil, fmt.Errorf("trojan: %w", err)
	}
	if ss == nil {
		ss = &xray.StreamSettings{}
	}
	if ss.Security == "" {
		ss.Security = "tls"
		ss.TLSSettings = &xray.TLSSettings{ServerName: n.Host}
	}
	return &xray.Outbound{
		Tag:      n.XrayTag(),
		Protocol: "trojan",
		Settings: trojanOutboundSettings{
			Servers: []trojanServer{{
				Address:  n.Host,
				Port:     n.Port,
				Password: u.User.Username(),
			}},
		},
		StreamSettings: ss,
	}, nil
}

// ── Shadowsocks outbound ──────────────────────────────────────────────────

type ssOutboundSettings struct {
	Servers []ssServer `json:"servers"`
}

type ssServer struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Method   string `json:"method"`
	Password string `json:"password"`
}

func ssOutbound(n *Node) (*xray.Outbound, error) {
	u, err := url.Parse(n.URI)
	if err != nil || u.User == nil {
		return nil, fmt.Errorf("ss: invalid URI: %w", err)
	}
	userinfoB64 := u.User.Username()
	decoded, err := base64.StdEncoding.DecodeString(userinfoB64)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(userinfoB64)
		if err != nil {
			decoded = []byte(userinfoB64)
		}
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("ss: cannot parse userinfo")
	}
	method, password := parts[0], parts[1]
	return &xray.Outbound{
		Tag:      n.XrayTag(),
		Protocol: "shadowsocks",
		Settings: ssOutboundSettings{
			Servers: []ssServer{{
				Address:  n.Host,
				Port:     n.Port,
				Method:   method,
				Password: password,
			}},
		},
	}, nil
}

// ── Hysteria2 outbound ────────────────────────────────────────────────────

type hy2OutboundSettings struct {
	Servers []hy2Server `json:"servers"`
}

type hy2Server struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Password string `json:"password"`
}

func hysteria2Outbound(n *Node) (*xray.Outbound, error) {
	u, err := url.Parse(n.URI)
	if err != nil || u.User == nil {
		return nil, fmt.Errorf("hysteria2: invalid URI: %w", err)
	}
	password := u.User.Username()
	q := u.Query()
	sni := q.Get("sni")
	if sni == "" {
		sni = n.Host
	}
	return &xray.Outbound{
		Tag:      n.XrayTag(),
		Protocol: "hysteria2",
		Settings: hy2OutboundSettings{
			Servers: []hy2Server{{
				Address:  n.Host,
				Port:     n.Port,
				Password: password,
			}},
		},
		StreamSettings: &xray.StreamSettings{
			Network:  "hysteria2",
			Security: "tls",
			TLSSettings: &xray.TLSSettings{
				ServerName:    sni,
				AllowInsecure: q.Get("insecure") == "1",
				ALPN:          []string{"h3"},
			},
		},
	}, nil
}

// ── shared helpers ────────────────────────────────────────────────────────

func parseStreamSettings(q url.Values, authority string) (*xray.StreamSettings, error) {
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	security := q.Get("security")
	sni := q.Get("sni")
	if sni == "" {
		sni = q.Get("host")
	}
	if sni == "" {
		if h, _, err := splitHostPortStr(authority); err == nil {
			sni = h
		}
	}
	fp := q.Get("fp")

	ss := &xray.StreamSettings{Network: network}

	switch security {
	case "tls":
		ss.Security = "tls"
		ss.TLSSettings = &xray.TLSSettings{
			ServerName:  sni,
			Fingerprint: fp,
		}
		if q.Get("alpn") != "" {
			ss.TLSSettings.ALPN = strings.Split(q.Get("alpn"), ",")
		}
	case "reality":
		ss.Security = "reality"
		ss.TLSSettings = &xray.TLSSettings{
			ServerName:  sni,
			Fingerprint: fp,
		}
	}

	switch network {
	case "ws":
		ss.WSSettings = &xray.WSSettings{
			Path:    q.Get("path"),
			Headers: headerIfNonEmpty("Host", q.Get("host")),
		}
	case "grpc":
		ss.GRPCSettings = &xray.GRPCSettings{ServiceName: q.Get("serviceName")}
	}
	return ss, nil
}

func headerIfNonEmpty(key, val string) map[string]string {
	if val == "" {
		return nil
	}
	return map[string]string{key: val}
}

func splitHostPortStr(hostport string) (host string, port int, err error) {
	colon := strings.LastIndex(hostport, ":")
	if colon < 0 {
		return hostport, 0, nil
	}
	p, e := strconv.Atoi(hostport[colon+1:])
	if e != nil {
		return "", 0, e
	}
	return hostport[:colon], p, nil
}
