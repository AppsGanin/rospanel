package selftest

import "github.com/AppsGanin/rospanel/internal/model"

// buildHysteria: Hysteria2 over QUIC/UDP, dialing the Hysteria inbound directly (no
// port-hopping for a loopback probe). The client structure is Xray's own, not the
// official-hysteria one: protocol "hysteria", target address/port inside settings,
// and — the gotcha — auth inside streamSettings.hysteriaSettings.auth, with ALPN
// pinned to ["h3"] or the QUIC handshake dies. Verified against Xray 26.6.27.
func buildHysteria(set *model.Settings, u model.User, socksPort int) (*clientConfig, error) {
	tls := tlsFor(set, []string{"h3"}, "") // QUIC: no uTLS fingerprint
	out := wireOutbound{
		Protocol: "hysteria",
		Settings: hysteriaOutSettings{
			Version: 2,
			Address: loopbackHost,
			Port:    set.HysteriaPort,
		},
		StreamSettings: &wireStream{
			Network:     "hysteria",
			Security:    "tls",
			TLSSettings: tls,
			HysteriaSet: &wireHysteria{Version: 2, Auth: u.Password},
		},
	}
	return assemble(socksPort, out)
}

// buildReality: VLESS + gRPC + REALITY. No flow (gRPC), security "reality" instead
// of "tls", and realitySettings carrying the donor SNI + public key + shortId +
// spiderX — mirroring the reality:// share-link. REALITY does its own cert dance, so
// there's no tlsSettings/pin here.
func buildReality(set *model.Settings, u model.User, socksPort int) (*clientConfig, error) {
	out := wireOutbound{
		Protocol: "vless",
		Settings: vlessOutSettings{VNext: []vlessServer{{
			Address: loopbackHost,
			Port:    set.RealityPort,
			Users:   []vlessUser{{ID: u.UUID, Encryption: "none"}}, // no flow for gRPC
		}}},
		StreamSettings: &wireStream{
			Network:      "grpc",
			Security:     "reality",
			GRPCSettings: &wireGRPC{ServiceName: set.RealityServiceName},
			RealitySettings: &wireReality{
				ServerName:  set.RealitySNI(),
				Fingerprint: set.RealityFP(),
				PublicKey:   set.RealityPublicKey,
				ShortID:     set.RealitySID(),
				SpiderX:     "/",
			},
		},
	}
	return assemble(socksPort, out)
}
