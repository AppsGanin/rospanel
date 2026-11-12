package tuning

import (
	"net"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestListenTCPInheritsUserTimeout(t *testing.T) {
	ln, err := ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	client, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	raw, err := server.(*net.TCPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var timeout, keepalive int
	var optErr error
	if err := raw.Control(func(fd uintptr) {
		timeout, optErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT)
		if optErr == nil {
			keepalive, optErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_KEEPALIVE)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if optErr != nil {
		t.Fatal(optErr)
	}
	if want := int(TCPUserTimeout.Milliseconds()); timeout != want {
		t.Errorf("accepted TCP_USER_TIMEOUT = %d, want %d", timeout, want)
	}
	if keepalive != 1 {
		t.Errorf("accepted SO_KEEPALIVE = %d, want 1", keepalive)
	}

	// The timeout measures unacknowledged data, not inactivity. Even with a
	// deliberately short timeout, a healthy idle peer must remain usable.
	if err := raw.Control(func(fd uintptr) {
		optErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, 100)
	}); err != nil {
		t.Fatal(err)
	}
	if optErr != nil {
		t.Fatal(optErr)
	}
	time.Sleep(200 * time.Millisecond)
	if err := server.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := server.Read(b[:]); err != nil {
		t.Fatalf("healthy idle peer was lost: %v", err)
	}
}
