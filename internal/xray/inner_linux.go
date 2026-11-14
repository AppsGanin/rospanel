//go:build linux

package xray

// Abstract unix sockets: see inner.go.
const (
	VLESSInnerAddr       = "@rospanel-vless"
	NaiveInnerAddr       = "@rospanel-naive"
	MieruInnerAddr       = "@rospanel-mieru"
	AnyTLSInnerAddr      = "@rospanel-anytls"
	RealityInnerAddr     = "@rospanel-reality"
	MTProtoInnerAddr     = "@rospanel-mtproto"
	TrustTunnelInnerAddr = "@rospanel-trusttunnel"
	XHTTPInnerAddr       = "@rospanel-xhttp"
)
