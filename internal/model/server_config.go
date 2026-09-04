package model

// ServerConfigSnapshot is the whole master server config a snapshot captures — the
// settings on the server-settings tabs plus the server's custom inbounds. Rollback
// restores all of it EXCEPT the certificate/domain identity (TLS mode, ACME, cert
// paths, host/SNI): rolling those back could break the live certificate, and they are
// the one thing an operator almost never means to undo. Those fields are captured too,
// for reference, but the restore path leaves them untouched.
type ServerConfigSnapshot struct {
	// Protocols + ports.
	VLESSEnabled    bool   `json:"vless_enabled"`
	HysteriaEnabled bool   `json:"hysteria_enabled"`
	RealityEnabled  bool   `json:"reality_enabled"`
	VLESSPort       int    `json:"vless_port"`
	HysteriaPort    int    `json:"hysteria_port"`
	HopStart        int    `json:"hop_start"`
	HopEnd          int    `json:"hop_end"`
	HopInterval     string `json:"hop_interval"`
	HysteriaObfs    string `json:"hysteria_obfs"`
	RealityPort     int    `json:"reality_port"`

	// REALITY identity (private key encrypted at rest via the snapshot blob).
	RealityDest        string `json:"reality_dest"`
	RealityPrivateKey  string `json:"reality_private_key"`
	RealityPublicKey   string `json:"reality_public_key"`
	RealityShortID     string `json:"reality_short_id"`
	RealityPath        string `json:"reality_path"`
	RealityMaxTimeDiff int    `json:"reality_max_time_diff"`
	VLESSFp            string `json:"vless_fp"`
	RealityFp          string `json:"reality_fp"`

	// Client-facing protocol names.
	VLESSName    string `json:"vless_name"`
	RealityName  string `json:"reality_name"`
	HysteriaName string `json:"hysteria_name"`

	// TLS/transport tweaks (NOT the cert mode).
	TLSFragment bool `json:"tls_fragment"`
	TLSMin13    bool `json:"tls_min13"`
	BlockQUIC   bool `json:"block_quic"`

	// Routing + egress.
	Routing        RoutingConfig `json:"routing"`
	WarpEnabled    bool          `json:"warp_enabled"`
	WarpPrivateKey string        `json:"warp_private_key"`
	WarpPublicKey  string        `json:"warp_public_key"`
	WarpEndpoint   string        `json:"warp_endpoint"`
	WarpAddressV4  string        `json:"warp_address_v4"`
	WarpAddressV6  string        `json:"warp_address_v6"`
	WarpReserved   string        `json:"warp_reserved"`
	OperaEnabled   bool          `json:"opera_enabled"`
	OperaCountry   string        `json:"opera_country"`
	OperaPort      int           `json:"opera_port"`

	// DNS + decoy.
	XrayDNS       string `json:"xray_dns"`
	DecoyTemplate string `json:"decoy_template"`

	// The server's custom inbounds (LocalNodeID).
	Inbounds []Inbound `json:"inbounds"`

	// Certificate / domain identity — captured for reference, NEVER restored.
	TLSMode      string `json:"tls_mode"`
	Host         string `json:"host"`
	SNI          string `json:"sni"`
	ACMEEmail    string `json:"acme_email"`
	ACMEProvider string `json:"acme_provider"`
}

// ServerConfigFrom captures the current server config into a snapshot payload.
func ServerConfigFrom(s *Settings, inbounds []Inbound) ServerConfigSnapshot {
	return ServerConfigSnapshot{
		VLESSEnabled:    s.VLESSEnabled,
		HysteriaEnabled: s.HysteriaEnabled,
		RealityEnabled:  s.RealityEnabled,
		VLESSPort:       s.VLESSPort,
		HysteriaPort:    s.HysteriaPort,
		HopStart:        s.HopStart,
		HopEnd:          s.HopEnd,
		HopInterval:     s.HopInterval,
		HysteriaObfs:    s.HysteriaObfs,
		RealityPort:     s.RealityPort,

		RealityDest:        s.RealityDest,
		RealityPrivateKey:  s.RealityPrivateKey,
		RealityPublicKey:   s.RealityPublicKey,
		RealityShortID:     s.RealityShortID,
		RealityPath:        s.RealityPath,
		RealityMaxTimeDiff: s.RealityMaxTimeDiff,
		VLESSFp:            s.VLESSFp,
		RealityFp:          s.RealityFp,

		VLESSName:    s.VLESSName,
		RealityName:  s.RealityName,
		HysteriaName: s.HysteriaName,

		TLSFragment: s.TLSFragment,
		TLSMin13:    s.TLSMin13,
		BlockQUIC:   s.BlockQUIC,

		Routing:        s.Routing,
		WarpEnabled:    s.WarpEnabled,
		WarpPrivateKey: s.WarpPrivateKey,
		WarpPublicKey:  s.WarpPublicKey,
		WarpEndpoint:   s.WarpEndpoint,
		WarpAddressV4:  s.WarpAddressV4,
		WarpAddressV6:  s.WarpAddressV6,
		WarpReserved:   s.WarpReserved,
		OperaEnabled:   s.OperaEnabled,
		OperaCountry:   s.OperaCountry,
		OperaPort:      s.OperaPort,

		XrayDNS:       s.XrayDNS,
		DecoyTemplate: s.DecoyTemplate,

		Inbounds: inbounds,

		TLSMode:      s.TLSMode,
		Host:         s.Host,
		SNI:          s.SNI,
		ACMEEmail:    s.ACMEEmail,
		ACMEProvider: s.ACMEProvider,
	}
}
