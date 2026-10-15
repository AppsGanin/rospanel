package xray

import (
	"net"
	"strconv"
	"strings"
)

// The lanes behind the panel's own listeners — the TCP-TLS lane behind the front,
// naive-in behind the NaiveProxy handler, mieru-in behind the mieru server, anytls-in
// behind the AnyTLS one, mtproto-in behind the Telegram proxy — listen on VLESSInnerAddr,
// NaiveInnerAddr, MieruInnerAddr, AnyTLSInnerAddr and MTProtoInnerAddr:
// abstract unix sockets on Linux (inner_linux.go), with no port to reserve or collide
// with, nothing reachable from the network, and no file a crash can leave behind;
// loopback ports elsewhere (inner_other.go), on a development box.

// InnerNetwork is the network to dial an inner address on.
func InnerNetwork(addr string) string {
	if strings.HasPrefix(addr, "@") {
		return "unix"
	}
	return "tcp"
}

// InnerPort is an inner address's loopback port; 0 for a socket.
func InnerPort(addr string) int {
	_, p := InnerListen(addr)
	return p
}

// InnerListen is an inner address as an inbound's listen and port.
func InnerListen(addr string) (string, int) {
	if InnerNetwork(addr) == "unix" {
		return addr, 0
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	p, _ := strconv.Atoi(port)
	return host, p
}

// innerAddr is the address an inbound listening on listen and port is dialed at.
func innerAddr(listen string, port int) string {
	if port == 0 {
		return listen
	}
	if listen == "" {
		listen = "127.0.0.1"
	}
	return net.JoinHostPort(listen, strconv.Itoa(port))
}
