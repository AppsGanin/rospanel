//go:build !linux

package xray

// No abstract sockets outside Linux: loopback ports (see inner.go), which custom
// inbounds cannot take (core.reservedPorts).
const (
	VLESSInnerAddr   = "127.0.0.1:18443"
	NaiveInnerAddr   = "127.0.0.1:18444"
	MieruInnerAddr   = "127.0.0.1:18445"
	AnyTLSInnerAddr  = "127.0.0.1:18446"
	MTProtoInnerAddr = "127.0.0.1:18448"
)
