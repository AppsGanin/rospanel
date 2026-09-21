//go:build !linux

package xray

import (
	"errors"
	"net/netip"
)

// tcpSock mirrors the Linux type; nothing here ever fills one.
type tcpSock struct {
	local  netip.AddrPort
	remote netip.AddrPort
}

var errNoSockDiag = errors.New("closing sockets needs Linux sock_diag")

func dumpTCP() ([]tcpSock, error) { return nil, errNoSockDiag }

func destroyTCP([]tcpSock) (int, error) { return 0, errNoSockDiag }
