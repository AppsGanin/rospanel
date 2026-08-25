package proxyproto

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestParseV1(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantIP   string
		wantPort int
		wantNil  bool
	}{
		{
			name:     "valid IPv4",
			line:     "PROXY TCP4 198.51.100.1 203.0.113.1 56324 443\r\n",
			wantIP:   "198.51.100.1",
			wantPort: 56324,
		},
		{
			name:     "valid IPv6",
			line:     "PROXY TCP6 2001:db8::1 2001:db8::2 65000 443\r\n",
			wantIP:   "2001:db8::1",
			wantPort: 65000,
		},
		{
			name:    "unknown protocol",
			line:    "PROXY UNKNOWN\r\n",
			wantNil: true,
		},
		{
			name:    "malformed prefix",
			line:    "NOTPROXY TCP4 1.2.3.4 5.6.7.8 1234 443\r\n",
			wantNil: true,
		},
		{
			name:    "too few fields",
			line:    "PROXY TCP4 1.2.3.4 5.6.7.8\r\n",
			wantNil: true,
		},
		{
			name:    "invalid IP",
			line:    "PROXY TCP4 999.999.999.999 1.2.3.4 1234 443\r\n",
			wantNil: true,
		},
		{
			name:    "invalid port",
			line:    "PROXY TCP4 1.2.3.4 5.6.7.8 notaport 443\r\n",
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := parseV1(tc.line)
			if tc.wantNil {
				if addr != nil {
					t.Fatalf("parseV1(%q) = %v; want nil", tc.line, addr)
				}
				return
			}
			if addr == nil {
				t.Fatalf("parseV1(%q) = nil; want %s:%d", tc.line, tc.wantIP, tc.wantPort)
			}
			tcpAddr, ok := addr.(*net.TCPAddr)
			if !ok {
				t.Fatalf("parseV1(%q) returned %T; want *net.TCPAddr", tc.line, addr)
			}
			if tcpAddr.IP.String() != tc.wantIP || tcpAddr.Port != tc.wantPort {
				t.Fatalf("parseV1(%q) = %s:%d; want %s:%d", tc.line, tcpAddr.IP, tcpAddr.Port, tc.wantIP, tc.wantPort)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	if isLoopback(nil) {
		t.Error("isLoopback(nil) = true; want false")
	}
	if !isLoopback(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}) {
		t.Error("isLoopback(127.0.0.1:8080) = false; want true")
	}
	if !isLoopback(&net.TCPAddr{IP: net.ParseIP("127.0.1.5"), Port: 9000}) {
		t.Error("isLoopback(127.0.1.5:9000) = false; want true")
	}
	if !isLoopback(&net.TCPAddr{IP: net.ParseIP("::1"), Port: 8080}) {
		t.Error("isLoopback(::1:8080) = false; want true")
	}
	if isLoopback(&net.TCPAddr{IP: net.ParseIP("192.168.1.10"), Port: 8080}) {
		t.Error("isLoopback(192.168.1.10:8080) = true; want false")
	}
	if isLoopback(&net.TCPAddr{IP: net.ParseIP("8.8.8.8"), Port: 53}) {
		t.Error("isLoopback(8.8.8.8:53) = true; want false")
	}
}

func TestListenerWithProxyHeader(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()

	pln := &Listener{Listener: ln}
	serverErr := make(chan error, 1)

	go func() {
		conn, err := pln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		// Check RemoteAddr
		remote := conn.RemoteAddr()
		if remote == nil || remote.String() != "198.51.100.1:56324" {
			t.Errorf("RemoteAddr() = %v; want 198.51.100.1:56324", remote)
		}

		// Read body payload
		buf := make([]byte, 16)
		n, err := io.ReadFull(conn, buf)
		if err != nil {
			serverErr <- err
			return
		}
		if string(buf[:n]) != "HELLO_WORLD_1234" {
			t.Errorf("read payload = %q; want %q", string(buf[:n]), "HELLO_WORLD_1234")
		}
		serverErr <- nil
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	defer client.Close()

	// Write PROXY v1 header + payload
	header := "PROXY TCP4 198.51.100.1 127.0.0.1 56324 8080\r\nHELLO_WORLD_1234"
	if _, err := client.Write([]byte(header)); err != nil {
		t.Fatalf("client write failed: %v", err)
	}

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("test timed out waiting for server")
	}
}

func TestListenerWithoutProxyHeader(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()

	pln := &Listener{Listener: ln}
	serverErr := make(chan error, 1)

	go func() {
		conn, err := pln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		// RemoteAddr should be actual loopback client transport address
		remote := conn.RemoteAddr()
		if remote == nil || !isLoopback(remote) {
			t.Errorf("RemoteAddr() = %v; want loopback address", remote)
		}

		// Payload without PROXY header should be read intact
		buf := make([]byte, 12)
		n, err := io.ReadFull(conn, buf)
		if err != nil {
			serverErr <- err
			return
		}
		if string(buf[:n]) != "PLAIN_HEADER" {
			t.Errorf("read payload = %q; want %q", string(buf[:n]), "PLAIN_HEADER")
		}
		serverErr <- nil
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("PLAIN_HEADER")); err != nil {
		t.Fatalf("client write failed: %v", err)
	}

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("test timed out waiting for server")
	}
}

type fakeConn struct {
	net.Conn
	fakeRemote net.Addr
	readBuf    *bytes.Buffer
}

func (f *fakeConn) RemoteAddr() net.Addr {
	return f.fakeRemote
}

func (f *fakeConn) Read(p []byte) (int, error) {
	return f.readBuf.Read(p)
}

func (f *fakeConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (f *fakeConn) Close() error {
	return nil
}

func TestNonLoopbackIgnoresProxyHeader(t *testing.T) {
	// Simulate non-loopback connection (e.g. 198.51.100.5) trying to send PROXY header
	nonLoopbackAddr := &net.TCPAddr{IP: net.ParseIP("198.51.100.5"), Port: 12345}
	payload := []byte("PROXY TCP4 1.1.1.1 2.2.2.2 1111 2222\r\nPAYLOAD")

	raw := &fakeConn{
		fakeRemote: nonLoopbackAddr,
		readBuf:    bytes.NewBuffer(payload),
	}

	c := &conn{Conn: raw, r: bufioNewReader(raw)}
	if c.RemoteAddr().String() != "198.51.100.5:12345" {
		t.Fatalf("RemoteAddr() = %v; want %v (spoof attempt must be ignored)", c.RemoteAddr(), nonLoopbackAddr)
	}

	// Payload must remain unconsumed in stream
	out := make([]byte, len(payload))
	n, err := io.ReadFull(c, out)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !bytes.Equal(out[:n], payload) {
		t.Fatalf("read payload = %q; want %q", out[:n], payload)
	}
}

func bufioNewReader(r io.Reader) *bufio.Reader {
	return bufio.NewReader(r)
}
