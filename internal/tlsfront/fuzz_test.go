package tlsfront

import "testing"

// The port reads bytes from anyone, so the name parser must survive whatever a probe
// sends — no panic, no hang — and either name a server or say "read on".
func FuzzClientHelloSNI(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("GET / HTTP/1.1\r\n"))
	f.Add([]byte{0x16, 0x03, 0x01, 0x00, 0x05, 'h', 'e', 'l', 'l', 'o'})
	f.Add(clientHello("max.ru"))
	f.Add(fragmentHello(clientHello("max.ru")))
	f.Fuzz(func(t *testing.T, b []byte) {
		sni, needMore := clientHelloSNI(b)
		if sni != "" && needMore {
			t.Fatalf("%q: a name and a request to read on at once", sni)
		}
	})
}
