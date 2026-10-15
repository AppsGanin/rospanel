package mtproto

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"time"

	"golang.org/x/crypto/curve25519"
)

// Fake TLS, as Telegram's clients speak it with an "ee" secret
// (https://core.telegram.org/mtproto/mtproto-transports#transport-obfuscation and
// tdlib's TlsInit): a TLS 1.3 ClientHello whose random is
//
//	HMAC-SHA256(secret, the whole record with the random zeroed) XOR (0…0 ‖ unix time LE)
//
// — so only the 28 leading bytes of the XOR are zero, and the last four are the time it
// was made. The server proves it knows the secret the same way in its own random, then
// both sides wrap the obfuscated MTProto stream in application-data records.

const (
	recordHandshake = 0x16
	recordCCS       = 0x14
	recordAppData   = 0x17

	helloClient = 0x01
	helloServer = 0x02

	randomLen = 32
	// record type(1) + version(2) + length(2) + handshake type(1) + length(3) + version(2)
	randomOffset = 11

	maxRecord        = 16384
	maxRecordPayload = maxRecord - 5
	// maxHelloRecords bounds how many records a ClientHello may be split into.
	maxHelloRecords = 10
)

var tlsVersion = [2]byte{3, 3}

// clientHello is what the server keeps of a ClientHello.
type clientHello struct {
	raw       []byte // the hello as one record, random zeroed: what the HMAC covers
	random    [randomLen]byte
	sessionID []byte
	cipher    uint16
	sni       []string
}

// readClientHello reads a ClientHello, which may come split over several handshake
// records, and parses what the handshake needs.
func readClientHello(r io.Reader) (*clientHello, error) {
	var body bytes.Buffer
	need := -1 // the handshake message's length once its header is in
	for records := 0; need < 0 || body.Len() < need; records++ {
		if records >= maxHelloRecords {
			return nil, errors.New("mtproto: ClientHello in too many records")
		}
		var hdr [5]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, err
		}
		if hdr[0] != recordHandshake || hdr[1] != 3 || hdr[2] != 1 {
			return nil, errors.New("mtproto: not a TLS ClientHello")
		}
		n := int(binary.BigEndian.Uint16(hdr[3:]))
		if n == 0 || n > maxRecord {
			return nil, errors.New("mtproto: bad record length")
		}
		if _, err := io.CopyN(&body, r, int64(n)); err != nil {
			return nil, err
		}
		if need < 0 && body.Len() >= 4 {
			b := body.Bytes()
			if b[0] != helloClient {
				return nil, errors.New("mtproto: not a ClientHello")
			}
			need = 4 + (int(b[1])<<16 | int(b[2])<<8 | int(b[3]))
			if need > 0xffff {
				return nil, errors.New("mtproto: ClientHello too long")
			}
		}
	}
	msg := body.Bytes()[:need]
	h := &clientHello{}
	// The record the HMAC covers is the message in one record, however it came.
	h.raw = make([]byte, 0, 5+len(msg))
	h.raw = append(h.raw, recordHandshake, 3, 1)
	h.raw = binary.BigEndian.AppendUint16(h.raw, uint16(len(msg)))
	h.raw = append(h.raw, msg...)
	if err := h.parse(msg[4:]); err != nil {
		return nil, err
	}
	clear(h.raw[randomOffset : randomOffset+randomLen])
	return h, nil
}

// parse reads the ClientHello body: version, random, session id, the first cipher suite
// that is not GREASE, and the server names.
func (h *clientHello) parse(b []byte) error {
	p := parser{b: b}
	p.skip(2) // legacy version
	copy(h.random[:], p.take(randomLen))
	sid := int(p.u8())
	if sid > 32 { // TLS caps it there; anything longer is no client of ours
		return errors.New("mtproto: malformed ClientHello")
	}
	h.sessionID = bytes.Clone(p.take(sid))
	suites := p.take(int(p.u16()))
	for i := 0; i+1 < len(suites); i += 2 {
		if cs := binary.BigEndian.Uint16(suites[i:]); cs&0x0f0f != 0x0a0a {
			h.cipher = cs
			break
		}
	}
	p.skip(int(p.u8())) // compression methods
	ext := parser{b: p.take(int(p.u16()))}
	if p.bad {
		return errors.New("mtproto: malformed ClientHello")
	}
	for len(ext.b) > 0 && !ext.bad {
		typ, data := ext.u16(), ext.take(int(ext.u16()))
		if typ != 0 { // server_name
			continue
		}
		list := parser{b: data}
		list = parser{b: list.take(int(list.u16()))}
		for len(list.b) > 0 && !list.bad {
			kind, name := list.u8(), list.take(int(list.u16()))
			if kind == 0 && !list.bad {
				h.sni = append(h.sni, string(name))
			}
		}
	}
	if ext.bad || h.cipher == 0 {
		return errors.New("mtproto: malformed ClientHello")
	}
	return nil
}

// verify checks the hello against one secret: the HMAC, then the time it carries. It
// returns false for a hello made with another secret.
func (h *clientHello) verify(secret []byte, now time.Time, skew time.Duration) bool {
	mac := hmac.New(sha256.New, secret)
	mac.Write(h.raw)
	sum := mac.Sum(nil)
	for i := range sum {
		sum[i] ^= h.random[i]
	}
	var zero [randomLen - 4]byte
	if subtle.ConstantTimeCompare(sum[:randomLen-4], zero[:]) != 1 {
		return false
	}
	made := time.Unix(int64(binary.LittleEndian.Uint32(sum[randomLen-4:])), 0)
	d := now.Sub(made)
	return d <= skew && d >= -skew
}

func (h *clientHello) hasSNI(name string) bool {
	for _, n := range h.sni {
		if n == name {
			return true
		}
	}
	return false
}

// serverHello is the server's flight: a TLS 1.3 ServerHello echoing the session id and
// the client's cipher, ChangeCipherSpec, and one application-data record standing for
// the encrypted rest of the handshake — sized like a certificate chain. Its random is the
// HMAC of the client's random and the flight with the random zeroed.
func serverHello(secret []byte, h *clientHello) []byte {
	var hs bytes.Buffer
	hs.Write(tlsVersion[:])
	hs.Write(make([]byte, randomLen))
	hs.WriteByte(byte(len(h.sessionID)))
	hs.Write(h.sessionID)
	_ = binary.Write(&hs, binary.BigEndian, h.cipher)
	hs.Write([]byte{
		0x00,       // no compression
		0x00, 0x2e, // extensions: 46 bytes
		0x00, 0x2b, 0x00, 0x02, 0x03, 0x04, // supported_versions: TLS 1.3
		0x00, 0x33, 0x00, 0x24, 0x00, 0x1d, 0x00, 0x20, // key_share: x25519, 32 bytes
	})
	var scalar [32]byte
	_, _ = rand.Read(scalar[:])
	pub, _ := curve25519.X25519(scalar[:], curve25519.Basepoint)
	hs.Write(pub)

	var out bytes.Buffer
	out.WriteByte(recordHandshake)
	out.Write(tlsVersion[:])
	_ = binary.Write(&out, binary.BigEndian, uint16(4+hs.Len()))
	out.WriteByte(helloServer)
	n := hs.Len()
	out.Write([]byte{byte(n >> 16), byte(n >> 8), byte(n)})
	out.Write(hs.Bytes())

	out.Write([]byte{recordCCS, 3, 3, 0, 1, 1})

	// Exactly one record: the client takes the HMAC over ServerHello, CCS and one
	// application-data record.
	noise := make([]byte, 2500+mrand.IntN(2200))
	_, _ = rand.Read(noise)
	out.WriteByte(recordAppData)
	out.Write(tlsVersion[:])
	_ = binary.Write(&out, binary.BigEndian, uint16(len(noise)))
	out.Write(noise)

	b := out.Bytes()
	mac := hmac.New(sha256.New, secret)
	mac.Write(h.random[:])
	mac.Write(b)
	copy(b[randomOffset:], mac.Sum(nil))
	return b
}

// recordConn carries a byte stream in TLS application-data records: Read takes the
// payload of each application-data record (skipping the client's ChangeCipherSpec),
// Write cuts what it is given into records.
type recordConn struct {
	r       *bufio.Reader
	w       io.Writer
	pending []byte
	wbuf    []byte // one record's worth, reused by every Write
}

func newRecordConn(r *bufio.Reader, w io.Writer) *recordConn {
	return &recordConn{r: r, w: w}
}

func (c *recordConn) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		var hdr [5]byte
		if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
			return 0, err
		}
		if hdr[1] != 3 || hdr[2] != 3 {
			return 0, fmt.Errorf("mtproto: bad record version %x%x", hdr[1], hdr[2])
		}
		n := int(binary.BigEndian.Uint16(hdr[3:]))
		if n > maxRecord+256 {
			return 0, errors.New("mtproto: record too long")
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return 0, err
		}
		switch hdr[0] {
		case recordAppData:
			c.pending = buf
		case recordCCS:
		default:
			return 0, fmt.Errorf("mtproto: unexpected record type %#x", hdr[0])
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *recordConn) Write(p []byte) (int, error) {
	written := 0
	if c.wbuf == nil {
		c.wbuf = make([]byte, 5+maxRecordPayload)
	}
	for len(p) > 0 {
		n := min(len(p), maxRecordPayload)
		buf := c.wbuf[:5+n]
		buf[0] = recordAppData
		buf[1], buf[2] = tlsVersion[0], tlsVersion[1]
		binary.BigEndian.PutUint16(buf[3:], uint16(n))
		copy(buf[5:], p[:n])
		if _, err := c.w.Write(buf); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// parser reads a TLS structure; a short read marks it bad instead of panicking.
type parser struct {
	b   []byte
	bad bool
}

func (p *parser) take(n int) []byte {
	if n < 0 || len(p.b) < n {
		p.bad, p.b = true, nil
		return nil
	}
	out := p.b[:n]
	p.b = p.b[n:]
	return out
}

func (p *parser) skip(n int) { p.take(n) }

func (p *parser) u8() byte {
	b := p.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (p *parser) u16() uint16 {
	b := p.take(2)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}
