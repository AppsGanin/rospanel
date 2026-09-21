package tlsfront

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// backend stands in for Xray: it records the first line (the PROXY header) of every
// connection and echoes back what follows it.
func backend(t *testing.T) (addr string, headers chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	headers = make(chan string, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				headers <- line
				_, _ = io.Copy(c, r)
			}()
		}
	}()
	return ln.Addr().String(), headers
}

// freePort is a port nothing listens on right now.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startFront(t *testing.T, target string) (*Front, string) {
	t.Helper()
	f := New(target)
	port := freePort(t)
	f.Ensure(port)
	t.Cleanup(f.Stop)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			return f, addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("the front never listened on %s: %v", addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Plain HTTP gets exactly what a Go HTTPS server answers, and never reaches Xray.
func TestPlainHTTPGetsTheGoAnswer(t *testing.T) {
	target, headers := backend(t)
	_, addr := startFront(t, target)
	for _, req := range []string{"GET / HTTP/1.1\r\nHost: x\r\n\r\n", "HEAD / HTTP/1.0\r\n\r\n", "OPTIONS * HTTP/1.1\r\n\r\n"} {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(c, req)
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		got, err := io.ReadAll(c)
		c.Close()
		if err != nil || string(got) != badRequest {
			t.Errorf("%q: got %q (%v), want %q", req, got, err, badRequest)
		}
	}
	select {
	case h := <-headers:
		t.Fatalf("plain HTTP reached Xray: %q", h)
	case <-time.After(100 * time.Millisecond):
	}
}

// Everything else goes to Xray whole, behind a PROXY header naming the client.
func TestTLSGoesToXrayWithTheClientAddress(t *testing.T) {
	target, headers := backend(t)
	_, addr := startFront(t, target)
	for _, first := range []string{"\x16\x03\x01\x02\x00rest-of-hello", "\x00\x01garbage"} {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(c, first); err != nil {
			t.Fatal(err)
		}
		select {
		case h := <-headers:
			local := c.LocalAddr().(*net.TCPAddr)
			want := fmt.Sprintf("PROXY TCP4 127.0.0.1 127.0.0.1 %d ", local.Port)
			if !strings.HasPrefix(h, want) || !strings.HasSuffix(h, "\r\n") {
				t.Errorf("PROXY header %q, want it to start %q", h, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%q never reached Xray", first)
		}
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		echo := make([]byte, len(first))
		if _, err := io.ReadFull(c, echo); err != nil || string(echo) != first {
			t.Errorf("echo %q (%v), want %q: the bytes read to sniff must reach Xray too", echo, err, first)
		}
		c.Close()
	}
}

// A port another process still holds is taken as soon as it is let go.
func TestEnsureWaitsForThePort(t *testing.T) {
	target, _ := backend(t)
	holder, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := holder.Addr().(*net.TCPAddr).Port
	f := New(target)
	t.Cleanup(f.Stop)
	f.Ensure(port)
	time.Sleep(200 * time.Millisecond)
	holder.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the front did not take the port once it was free: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.Ensure(0)
	if f.Port() != 0 {
		t.Fatal("Ensure(0) left the front on a port")
	}
}

// scripted is a backend that does one thing to every connection once it has read the
// PROXY header and the client's first bytes.
func scripted(t *testing.T, act func(c *net.TCPConn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				buf := make([]byte, 512)
				_, _ = c.Read(buf)
				act(c.(*net.TCPConn))
			}()
		}
	}()
	return ln.Addr().String()
}

func dialAndSend(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := io.WriteString(c, "\x16\x03\x01\x00\x05hello"); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	return c
}

// Xray resetting a connection resets the client too: FIN and RST are what an active
// prober tells servers apart by.
func TestXrayResetReachesTheClient(t *testing.T) {
	target := scripted(t, func(c *net.TCPConn) {
		_ = c.SetLinger(0)
		c.Close()
	})
	_, addr := startFront(t, target)
	c := dialAndSend(t, addr)
	_, err := io.ReadAll(c)
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("client read after Xray's reset: %v, want connection reset", err)
	}
}

// Xray closing ends the client's connection whole, even for a client that never
// closes its own side.
func TestXrayCloseEndsTheClient(t *testing.T) {
	target := scripted(t, func(c *net.TCPConn) {
		_, _ = io.WriteString(c, "bye")
		c.Close()
	})
	_, addr := startFront(t, target)
	c := dialAndSend(t, addr)
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "bye" {
		t.Fatalf("read %q (%v), want %q then EOF", got, err, "bye")
	}
}

// With nothing behind the front the client is reset, not left hanging.
func TestNoXrayResetsTheClient(t *testing.T) {
	gone := freePort(t)
	_, addr := startFront(t, fmt.Sprintf("127.0.0.1:%d", gone))
	c := dialAndSend(t, addr)
	if _, err := io.ReadAll(c); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("client read with no Xray: %v, want connection reset", err)
	}
}

// The front takes IPv6 clients as Xray did: its "0.0.0.0" was a dual-stack socket.
func TestFrontListensOnIPv6(t *testing.T) {
	probe, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback here: %v", err)
	}
	probe.Close()
	target, headers := backend(t)
	f, _ := startFront(t, target)
	c, err := net.Dial("tcp6", fmt.Sprintf("[::1]:%d", f.Port()))
	if err != nil {
		t.Fatalf("IPv6 client refused: %v", err)
	}
	defer c.Close()
	_, _ = io.WriteString(c, "\x16\x03\x01\x00\x05hello")
	select {
	case h := <-headers:
		if !strings.HasPrefix(h, "PROXY TCP6 ::1 ::1 ") {
			t.Fatalf("PROXY header %q, want TCP6 from ::1", h)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the IPv6 client never reached Xray")
	}
}
