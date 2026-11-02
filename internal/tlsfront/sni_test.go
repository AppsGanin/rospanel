package tlsfront

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// clientHello builds a TLS record holding a whole ClientHello — the parts the front
// reads, around a name: version, random, session id, ciphers, compression and the
// server_name extension (absent when sni is empty, which is a client dialing an address).
func clientHello(sni string) []byte {
	var exts []byte
	if sni != "" {
		name := []byte(sni)
		entry := append([]byte{nameTypeHostName, byte(len(name) >> 8), byte(len(name))}, name...)
		list := append([]byte{byte(len(entry) >> 8), byte(len(entry))}, entry...)
		exts = append(exts, 0, 0, byte(len(list)>>8), byte(len(list)))
		exts = append(exts, list...)
	}
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...) // random
	body = append(body, 0)                   // session id
	body = append(body, 0, 6, 0x13, 0x01, 0x13, 0x02, 0x13, 0x03)
	body = append(body, 1, 0) // compression: null
	body = append(body, byte(len(exts)>>8), byte(len(exts)))
	body = append(body, exts...)
	hs := append([]byte{handshakeHello, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
	return append([]byte{recordHandshake, 0x03, 0x01, byte(len(hs) >> 8), byte(len(hs))}, hs...)
}

// fragmentHello splits a ClientHello over two handshake records, the way a message with
// a big post-quantum key share arrives.
func fragmentHello(t *testing.T, hello []byte) []byte {
	t.Helper()
	hs := hello[5:]
	cut := len(hs) / 2
	var out []byte
	for _, part := range [][]byte{hs[:cut], hs[cut:]} {
		out = append(out, recordHandshake, 0x03, 0x01, byte(len(part)>>8), byte(len(part)))
		out = append(out, part...)
	}
	return out
}

func TestClientHelloSNI(t *testing.T) {
	for _, c := range []struct {
		name string
		sni  string
	}{
		{"donor", "max.ru"},
		{"own name", "vpn.example.com"},
		{"no name at all", ""},
	} {
		got, needMore := clientHelloSNI(clientHello(c.sni))
		if needMore || got != c.sni {
			t.Errorf("%s: got %q needMore=%v, want %q", c.name, got, needMore, c.sni)
		}
	}
}

// The message may arrive as several records; its name is still the name.
func TestClientHelloSNIOverSeveralRecords(t *testing.T) {
	hello := clientHello("max.ru")
	got, needMore := clientHelloSNI(fragmentHello(t, hello))
	if needMore || got != "max.ru" {
		t.Fatalf("got %q needMore=%v, want max.ru", got, needMore)
	}
}

// A ClientHello cut short is "not yet", never a wrong name.
func TestClientHelloSNIPartial(t *testing.T) {
	hello := clientHello("max.ru")
	for n := 0; n < len(hello); n++ {
		got, needMore := clientHelloSNI(hello[:n])
		if got != "" {
			t.Fatalf("at %d/%d bytes: named %q, want nothing yet", n, len(hello), got)
		}
		if !needMore {
			t.Fatalf("at %d/%d bytes: final, want to read on", n, len(hello))
		}
	}
}

func TestClientHelloSNINotATLSHandshake(t *testing.T) {
	for _, c := range []struct {
		name     string
		b        []byte
		needMore bool
	}{
		{"plain HTTP", []byte("GET / HTTP/1.1\r\n"), false},
		{"garbage", []byte{0x00, 0x01, 0x02}, false},
		{"one byte", []byte{recordHandshake}, true},
		{"a whole record, not a ClientHello", []byte{recordHandshake, 0x03, 0x01, 0, 5, 'h', 'e', 'l', 'l', 'o'}, false},
		{"an alert", []byte{0x15, 0x03, 0x01, 0, 1, 0}, false},
		{"a record still arriving", []byte{recordHandshake, 0x03, 0x01, 0, 9, 1, 0}, true},
	} {
		got, needMore := clientHelloSNI(c.b)
		if got != "" || needMore != c.needMore {
			t.Errorf("%s: got %q needMore=%v, want %v", c.name, got, needMore, c.needMore)
		}
	}
}

// A name is matched without regard to case, as TLS compares server names, and only the
// names the operator listed go to REALITY.
func TestShareRoutesBySNI(t *testing.T) {
	tlsTarget, tlsHeaders := backend(t)
	realityTarget, realityHeaders := backend(t)
	addr := startSharedFront(t, tlsTarget, realityTarget, []string{"max.ru", "hh.ru"})

	for _, c := range []struct {
		sni     string
		reality bool
	}{
		{"max.ru", true},
		{"MAX.RU", true},
		{"hh.ru", true},
		{"vpn.example.com", false},
		{"", false},
	} {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(clientHello(c.sni)); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		echo := make([]byte, len(clientHello(c.sni)))
		if _, err := io.ReadFull(conn, echo); err != nil {
			t.Fatalf("%q: %v", c.sni, err)
		}
		conn.Close()
		ch := tlsHeaders
		if c.reality {
			ch = realityHeaders
		}
		var got string
		select {
		case got = <-ch:
		case <-time.After(3 * time.Second):
			t.Fatalf("%q never reached its backend", c.sni)
		}
		if !strings.Contains(got, "PROXY TCP4 127.0.0.1 127.0.0.1 ") {
			t.Errorf("%q: went in with %q, want a PROXY header", c.sni, got)
		}
	}
}

// Nothing shared: every connection goes where it went before.
func TestShareStopsWithNoNames(t *testing.T) {
	tlsTarget, tlsHeaders := backend(t)
	realityTarget, realityHeaders := backend(t)
	f := New(tlsTarget)
	f.Share(realityTarget, []string{"max.ru"})
	f.Share("", nil)
	addr := ensureFront(t, f)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write(clientHello("max.ru")); err != nil {
		t.Fatal(err)
	}
	if got := <-tlsHeaders; !strings.HasPrefix(got, "PROXY ") {
		t.Fatalf("header %q", got)
	}
	select {
	case h := <-realityHeaders:
		t.Fatalf("still split: REALITY took %q", h)
	case <-time.After(100 * time.Millisecond):
	}
}

func startSharedFront(t *testing.T, target, reality string, snis []string) string {
	t.Helper()
	f := New(target)
	f.Share(reality, snis)
	return ensureFront(t, f)
}

// ensureFront starts f on a free port and waits until it answers there.
func ensureFront(t *testing.T, f *Front) string {
	t.Helper()
	port := freePort(t)
	f.Ensure(port)
	t.Cleanup(f.Stop)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("the front never listened on %s: %v", addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
