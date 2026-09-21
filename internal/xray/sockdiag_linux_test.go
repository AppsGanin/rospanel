//go:build linux

package xray

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"syscall"
	"testing"
	"time"
)

// TestDestroyTCPEndsTheConnection closes the server side of a loopback connection the
// way a removed user's is closed, and checks the client sees it end. It needs
// CAP_NET_ADMIN and a kernel with CONFIG_INET_DIAG_DESTROY, so it skips without them.
func TestDestroyTCPEndsTheConnection(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	client, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()

	socks, err := dumpTCP()
	if err != nil {
		t.Skipf("socket table not readable here: %v", err)
	}
	local := netip.MustParseAddrPort(ln.Addr().String())
	remote := netip.MustParseAddrPort(client.LocalAddr().String())
	var hit []tcpSock
	for _, s := range socks {
		if s.local == local && s.remote == remote {
			hit = append(hit, s)
		}
	}
	if len(hit) != 1 {
		t.Fatalf("found %d server sockets for %v ← %v, want 1", len(hit), local, remote)
	}
	n, err := destroyTCP(hit)
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EOPNOTSUPP) {
		t.Skipf("cannot destroy sockets here: %v", err)
	}
	if err != nil || n != 1 {
		t.Fatalf("destroyTCP = %d, %v; want 1, nil", n, err)
	}
	// Both ends must see the connection end, and the side holding the socket — Xray, in
	// the real thing — an error rather than a clean close: that is what drops the flow.
	for name, c := range map[string]net.Conn{"client": client, "server": server} {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err := c.Read(make([]byte, 1))
		if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("%s read after the destroy: %v, want the connection ended", name, err)
		}
		if name == "server" && err == io.EOF {
			t.Fatalf("server read after the destroy: clean EOF, want an error")
		}
	}
}
