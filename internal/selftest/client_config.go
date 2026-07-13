package selftest

import (
	"encoding/json"

	"github.com/AppsGanin/rospanel/internal/model"
)

// This file builds the throwaway CLIENT Xray config for each protocol: a local
// SOCKS inbound in front of that protocol's outbound, aimed at our own inbound over
// loopback. The parameters mirror the share-links in internal/link exactly — the
// point of the test is to exercise the same handshake a real client would.
//
// One deliberate departure from the share-link: address is 127.0.0.1, not the
// public host — we're dialing our own inbound from the same box, and loopback
// avoids a DNS/hairpin dependency.
//
// TLS trust is reproduced EXACTLY as a real client sees it, on purpose. When the
// cert isn't CA-trusted (self-signed / LE IP cert), the share-link carries a
// cert pin (pcs=<hex>) and so does the probe — via pinnedPeerCertSha256, the field
// current Xray honours (allowInsecure was removed in v26, verified empirically). On
// a real CA cert TLSPinSHA256 is empty and normal serverName verification applies,
// which still passes over loopback because the cert's SAN matches the SNI, not the
// dialed address. So a probe failing on TLS means a real client would fail too.

// clientConfig is the marshaled config plus the loopback bits are already baked in.
type clientConfig struct {
	json []byte
}

// loopbackHost is what every outbound dials: our own inbound, on this machine.
const loopbackHost = "127.0.0.1"

// clientFingerprint is the uTLS fingerprint the probe presents. A fixed value is
// fine — the test cares that the handshake completes, not that it mimics a specific
// browser.
const clientFingerprint = "chrome"

// visionFlow is the VLESS flow for raw-TCP Vision (mirrors xray.VisionFlow, kept
// local so the probe doesn't depend on the supervisor package).
const visionFlow = "xtls-rprx-vision"

// tlsFor builds the client tlsSettings for a stream, pinning the cert exactly like
// the share-link's pcs param when the server cert isn't CA-trusted. The pin is the
// hex SHA-256 Xray reads from pinnedPeerCertSha256.
func tlsFor(set *model.Settings, alpn []string, fingerprint string) *wireTLS {
	t := &wireTLS{ServerName: set.SNI, ALPN: alpn, Fingerprint: fingerprint}
	if set.TLSPinSHA256 != "" {
		t.PinnedPeerCertSha256 = set.TLSPinSHA256
	}
	return t
}

// wire is the top-level client config shape.
type wire struct {
	Log       wireLog        `json:"log"`
	Inbounds  []wireInbound  `json:"inbounds"`
	Outbounds []wireOutbound `json:"outbounds"`
}

type wireLog struct {
	Loglevel string `json:"loglevel"`
}

type wireInbound struct {
	Listen   string          `json:"listen"`
	Port     int             `json:"port"`
	Protocol string          `json:"protocol"`
	Settings socksInSettings `json:"settings"`
}

type socksInSettings struct {
	Auth string `json:"auth"`
	UDP  bool   `json:"udp"`
}

type wireOutbound struct {
	Protocol       string      `json:"protocol"`
	Settings       any         `json:"settings"`
	StreamSettings *wireStream `json:"streamSettings,omitempty"`
}

type wireStream struct {
	Network         string        `json:"network"`
	Security        string        `json:"security,omitempty"`
	TLSSettings     *wireTLS      `json:"tlsSettings,omitempty"`
	RealitySettings *wireReality  `json:"realitySettings,omitempty"`
	WSSettings      *wireWS       `json:"wsSettings,omitempty"`
	GRPCSettings    *wireGRPC     `json:"grpcSettings,omitempty"`
	HysteriaSet     *wireHysteria `json:"hysteriaSettings,omitempty"`
}

type wireTLS struct {
	ServerName           string   `json:"serverName"`
	PinnedPeerCertSha256 string   `json:"pinnedPeerCertSha256,omitempty"`
	Fingerprint          string   `json:"fingerprint,omitempty"`
	ALPN                 []string `json:"alpn,omitempty"`
}

type wireReality struct {
	ServerName  string `json:"serverName"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId"`
	SpiderX     string `json:"spiderX,omitempty"`
}

type wireWS struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
}

type wireGRPC struct {
	ServiceName string `json:"serviceName"`
}

// wireHysteria is the streamSettings.hysteriaSettings block. Xray checks version==2
// here INDEPENDENTLY of the outbound settings block — omit it and the client fails
// to load with "version != 2". Auth is the per-user password.
type wireHysteria struct {
	Version int    `json:"version"`
	Auth    string `json:"auth,omitempty"`
}

// vnext outbound settings (VLESS).
type vlessOutSettings struct {
	VNext []vlessServer `json:"vnext"`
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

// servers outbound settings (Trojan).
type serversOutSettings struct {
	Servers []serverEntry `json:"servers"`
}

type serverEntry struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Password string `json:"password"`
}

// hysteriaOutSettings is the Hysteria2 client "settings" block. Unlike Trojan/VLESS,
// Xray puts the target address/port HERE (not in a servers/vnext list) and the auth
// goes into streamSettings.hysteriaSettings.auth — verified against Xray 26.6.27.
type hysteriaOutSettings struct {
	Version int    `json:"version"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// assemble finishes a client config: wraps the outbound with the local SOCKS inbound
// and marshals it.
func assemble(socksPort int, out wireOutbound) (*clientConfig, error) {
	w := wire{
		Log: wireLog{Loglevel: "warning"},
		Inbounds: []wireInbound{{
			Listen:   loopbackHost,
			Port:     socksPort,
			Protocol: "socks",
			Settings: socksInSettings{Auth: "noauth", UDP: true},
		}},
		Outbounds: []wireOutbound{out},
	}
	b, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	return &clientConfig{json: b}, nil
}

// buildVLESS: raw TCP + TLS + Vision, dialing the :443 inbound.
func buildVLESS(set *model.Settings, u model.User, socksPort int) (*clientConfig, error) {
	out := wireOutbound{
		Protocol: "vless",
		Settings: vlessOutSettings{VNext: []vlessServer{{
			Address: loopbackHost,
			Port:    set.VLESSPort,
			Users:   []vlessUser{{ID: u.UUID, Encryption: "none", Flow: visionFlow}},
		}}},
		StreamSettings: &wireStream{
			Network:     "tcp",
			Security:    "tls",
			TLSSettings: tlsFor(set, []string{"h2", "http/1.1"}, clientFingerprint),
		},
	}
	return assemble(socksPort, out)
}

// buildTrojan: Trojan-over-WS reached through the VLESS fallback on :443 (so it
// dials VLESSPort, not the loopback Trojan port), matching the share-link.
func buildTrojan(set *model.Settings, u model.User, socksPort int) (*clientConfig, error) {
	out := wireOutbound{
		Protocol: "trojan",
		Settings: serversOutSettings{Servers: []serverEntry{{
			Address:  loopbackHost,
			Port:     set.VLESSPort,
			Password: u.Password,
		}}},
		StreamSettings: &wireStream{
			Network:     "ws",
			Security:    "tls",
			TLSSettings: tlsFor(set, []string{"http/1.1"}, clientFingerprint),
			WSSettings: &wireWS{
				Path:    set.WSPath,
				Headers: map[string]string{"Host": set.SNI},
			},
		},
	}
	return assemble(socksPort, out)
}
