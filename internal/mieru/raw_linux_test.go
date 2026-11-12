package mieru

import (
	"context"
	"net"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/AppsGanin/rospanel/internal/tuning"
)

func TestListenerPreservesReuseAndBoundsTCPStalls(t *testing.T) {
	ln, err := newListenerFactory(newRawConns()).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	raw, err := ln.(*trackedListener).Listener.(*net.TCPListener).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var optErr error
	var timeout, reuse int
	if err := raw.Control(func(fd uintptr) {
		timeout, optErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT)
		if optErr == nil {
			reuse, optErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if optErr != nil {
		t.Fatal(optErr)
	}
	if timeout != int(tuning.TCPUserTimeout.Milliseconds()) || reuse != 1 {
		t.Fatalf("TCP_USER_TIMEOUT=%d SO_REUSEPORT=%d, want %d and 1", timeout, reuse, tuning.TCPUserTimeout.Milliseconds())
	}
}
