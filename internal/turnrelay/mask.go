package turnrelay

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// The masked wire of Free Turn Proxy (samosvalishe/free-turn-proxy, "-obf-profile").
//
// The call service shapes a relay whose payload does not look like a call's media: a
// DTLS record inside a TURN channel is the tell, and plain vk-turn-proxy traffic is held
// to a couple of megabits for it. Free Turn Proxy's clients seal every datagram — the
// DTLS records whole, handshake included — as an RTP/Opus packet: an RTP header (with
// the extensions a WebRTC audio stream carries, in the later profiles), an explicit
// nonce, and the record under ChaCha20-Poly1305 with the header as associated data.
//
// This is an independent implementation of that wire format for the server side, from
// the protocol as the free-turn-proxy source defines it; the tests check it against that
// source's own codecs. The three profiles differ only in the header:
//
//	rtpopus   [RTP 12 | nonce 12 | ciphertext | tag 16]                         V=2, no extension
//	rtpopus2  [RTP 12 | ext 12 (audio level, transport-cc) | nonce 12 | ct | tag] X=1, 2 ext words
//	rtpopus3  [RTP 12 | ext 16 (+ abs-send-time) | nonce 12 | ct | tag]           X=1, 3 ext words
//
// A client is tied to one profile and one key, so the relay reads the profile off the
// first byte and the extension length, and answers each client in the profile it spoke.
// Plain DTLS starts with a record content type (20–26), never 0x80 or 0x90, which is how
// the same port keeps serving vk-turn-proxy's unmasked clients.

// MaskKeyLen is the length of a masking key: a ChaCha20-Poly1305 key.
const MaskKeyLen = chacha20poly1305.KeySize

// maskProfile is the wire a connection speaks.
type maskProfile int32

const (
	profileUnknown maskProfile = iota // nothing received yet
	profilePlain                      // unmasked DTLS
	profileRTPOpus
	profileRTPOpus2
	profileRTPOpus3
)

const (
	rtpHeaderLen = 12
	maskNonceLen = chacha20poly1305.NonceSize
	maskTagLen   = chacha20poly1305.Overhead
	opusPT       = 0x6F // dynamic payload type 111, what a WebRTC Opus stream uses
	rtpMarkerBit = 0x80

	extAudioLevel   = 0x10 // one-byte extension id 1, length 1
	extTransportCC  = 0x21 // id 2, length 2
	extAbsSendTime  = 0x32 // id 3, length 3
	opusFrameTS     = 960  // 20 ms at 48 kHz
	opusFrameTS10ms = 480
	opusFrameTS40ms = 1920
)

// headerLen is the length of everything ahead of the ciphertext, the associated data.
func (p maskProfile) headerLen() int {
	switch p {
	case profileRTPOpus:
		return rtpHeaderLen + maskNonceLen
	case profileRTPOpus2:
		return rtpHeaderLen + 12 + maskNonceLen
	case profileRTPOpus3:
		return rtpHeaderLen + 16 + maskNonceLen
	}
	return 0
}

// detectProfile names the wire of one datagram. A masked profile is only a claim until
// the AEAD opens.
func detectProfile(b []byte) maskProfile {
	if len(b) == 0 {
		return profileUnknown
	}
	switch {
	case b[0] == 0x80:
		return profileRTPOpus
	case b[0] == 0x90 && len(b) >= 16 && b[12] == 0xBE && b[13] == 0xDE:
		switch binary.BigEndian.Uint16(b[14:16]) {
		case 2:
			return profileRTPOpus2
		case 3:
			return profileRTPOpus3
		}
	case b[0] >= 20 && b[0] <= 26:
		return profilePlain
	}
	return profileUnknown
}

// unmask opens a masked datagram in place and returns the DTLS bytes inside it.
func unmask(aead cipher.AEAD, p maskProfile, b []byte) ([]byte, error) {
	h := p.headerLen()
	if h == 0 || len(b) < h+maskTagLen {
		return nil, errors.New("turnrelay: masked datagram too short")
	}
	nonce := b[h-maskNonceLen : h]
	return aead.Open(b[h:h], nonce, b[h:], b[:h])
}

// maskSender is one connection's server-side RTP state: what its next packet's header
// says. The server sets the top bit of the nonce's session id (and, in the first
// profile, of the SSRC), the clients clear it, so the two directions never share a
// nonce under the one key.
type maskSender struct {
	aead    cipher.AEAD
	profile maskProfile
	start   time.Time

	mu        sync.Mutex
	sessionID [4]byte
	ssrc      [4]byte
	counter   uint64
	seq       uint16
	timestamp uint32
	tcc       uint16
	marked    bool // rtpopus2: the first packet carries the marker

	// rtpopus3's voice-activity pattern: stretches of speech and silence, and the
	// occasional sequence gap a lossy network leaves.
	speaking bool
	inState  int
	switchAt int
	nextGap  int
	gapSize  int
}

func newMaskSender(aead cipher.AEAD, p maskProfile) *maskSender {
	s := &maskSender{aead: aead, profile: p, start: time.Now()}
	var r [16]byte
	_, _ = rand.Read(r[:])
	copy(s.sessionID[:], r[0:4])
	copy(s.ssrc[:], r[4:8])
	s.sessionID[0] |= 0x80
	if p == profileRTPOpus {
		s.ssrc[0] |= 0x80
	}
	s.seq = binary.BigEndian.Uint16(r[8:10])
	s.timestamp = binary.BigEndian.Uint32(r[10:14])
	s.tcc = binary.BigEndian.Uint16(r[14:16])
	var c [8]byte
	_, _ = rand.Read(c[:])
	s.counter = binary.BigEndian.Uint64(c[:])
	s.speaking = true
	s.switchAt = randBetween(30, 200)
	s.nextGap = randBetween(50, 150)
	s.gapSize = randBetween(1, 3)
	return s
}

// randBetween is a uniform int in [lo, hi].
func randBetween(lo, hi int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(hi-lo+1)))
	if err != nil {
		return lo
	}
	return lo + int(n.Int64())
}

// seal writes payload into dst, which has room for it, as one masked datagram.
func (s *maskSender) seal(dst, payload []byte) []byte {
	h := s.profile.headerLen()
	out := dst[:h+len(payload)+maskTagLen]

	s.mu.Lock()
	seq, ts, tcc, ctr := s.seq, s.timestamp, s.tcc, s.counter
	s.seq++
	s.counter++
	s.tcc++
	marker := false
	level := byte(0)
	switch s.profile {
	case profileRTPOpus, profileRTPOpus2:
		s.timestamp += opusFrameTS
		if s.profile == profileRTPOpus2 && !s.marked {
			s.marked, marker = true, true
		}
	case profileRTPOpus3:
		marker = s.nextVoiceState()
		if s.speaking {
			level = 0x80 | byte(randBetween(20, 50))
		} else {
			level = byte(randBetween(100, 127))
		}
		if s.nextGap--; s.nextGap <= 0 {
			s.seq += uint16(s.gapSize)
			s.nextGap, s.gapSize = randBetween(50, 150), randBetween(1, 3)
		}
		switch r := randBetween(0, 255); {
		case r < 10:
			s.timestamp += opusFrameTS10ms
		case r < 230:
			s.timestamp += opusFrameTS
		default:
			s.timestamp += opusFrameTS40ms
		}
	}
	s.mu.Unlock()

	pt := byte(opusPT)
	if marker {
		pt |= rtpMarkerBit
	}
	out[0], out[1] = 0x80, pt
	binary.BigEndian.PutUint16(out[2:4], seq)
	binary.BigEndian.PutUint32(out[4:8], ts)
	copy(out[8:12], s.ssrc[:])
	switch s.profile {
	case profileRTPOpus2:
		out[0] = 0x90
		out[12], out[13] = 0xBE, 0xDE
		binary.BigEndian.PutUint16(out[14:16], 2)
		out[16], out[17] = extAudioLevel, 0x80|byte(seq&0x3F)
		out[18] = extTransportCC
		binary.BigEndian.PutUint16(out[19:21], tcc)
		out[21], out[22], out[23] = 0, 0, 0
	case profileRTPOpus3:
		out[0] = 0x90
		out[12], out[13] = 0xBE, 0xDE
		binary.BigEndian.PutUint16(out[14:16], 3)
		out[16], out[17] = extAudioLevel, level
		out[18] = extTransportCC
		binary.BigEndian.PutUint16(out[19:21], tcc)
		out[21] = extAbsSendTime
		ms := time.Since(s.start).Milliseconds()
		ast := uint32((ms/1000)%64)<<18 | uint32((ms%1000)<<18/1000)
		out[22], out[23], out[24] = byte(ast>>16), byte(ast>>8), byte(ast)
		out[25], out[26], out[27] = 0, 0, 0
	}
	nonce := out[h-maskNonceLen : h]
	copy(nonce[:4], s.sessionID[:])
	binary.BigEndian.PutUint64(nonce[4:], ctr)
	s.aead.Seal(out[h:h], nonce, payload, out[:h])
	return out
}

// nextVoiceState advances rtpopus3's speech/silence pattern by one packet and reports
// whether this packet starts a talk spurt, which RTP marks.
func (s *maskSender) nextVoiceState() bool {
	if s.inState++; s.inState < s.switchAt {
		return false
	}
	s.inState = 0
	if s.speaking {
		s.speaking, s.switchAt = false, randBetween(5, 30)
		return false
	}
	s.speaking, s.switchAt = true, randBetween(30, 200)
	return true
}

// maskedConn is one client's datagrams with the mask taken off on the way in and put
// back, in the client's own profile, on the way out.
type maskedConn struct {
	net.PacketConn
	aead    cipher.AEAD // nil: no key, only plain DTLS is served
	profile atomic.Int32
	sender  atomic.Pointer[maskSender]
	// rbuf is the receive buffer. The DTLS connection reads from one goroutine, so one
	// buffer per connection serves every read.
	rbuf []byte
}

// maskWriteBufs holds the buffers masked datagrams are sealed into: writes can come from
// the handshake and the tunnel at once, so they are pooled rather than per connection.
var maskWriteBufs = sync.Pool{New: func() any {
	b := make([]byte, packetSize+128)
	return &b
}}

func (c *maskedConn) ReadFrom(p []byte) (int, net.Addr, error) {
	if c.rbuf == nil {
		c.rbuf = make([]byte, packetSize+128)
	}
	buf := c.rbuf
	for {
		n, addr, err := c.PacketConn.ReadFrom(buf)
		if err != nil {
			return 0, addr, err
		}
		prof := detectProfile(buf[:n])
		switch prof {
		case profilePlain:
			c.profile.CompareAndSwap(int32(profileUnknown), int32(profilePlain))
			return copy(p, buf[:n]), addr, nil
		case profileRTPOpus, profileRTPOpus2, profileRTPOpus3:
			if c.aead == nil {
				continue
			}
			plain, err := unmask(c.aead, prof, buf[:n])
			if err != nil {
				continue // not ours: a wrong key or noise
			}
			if c.profile.CompareAndSwap(int32(profileUnknown), int32(prof)) {
				c.sender.Store(newMaskSender(c.aead, prof))
			}
			return copy(p, plain), addr, nil
		}
		// Anything else is not a datagram of either wire.
	}
}

func (c *maskedConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	s := c.sender.Load()
	if s == nil {
		return c.PacketConn.WriteTo(p, addr)
	}
	bp := maskWriteBufs.Get().(*[]byte)
	defer maskWriteBufs.Put(bp)
	if need := s.profile.headerLen() + len(p) + maskTagLen; cap(*bp) < need {
		*bp = make([]byte, need)
	}
	if _, err := c.PacketConn.WriteTo(s.seal((*bp)[:cap(*bp)], p), addr); err != nil {
		return 0, err
	}
	return len(p), nil
}

// maskedListener hands the DTLS server each client's datagrams through a maskedConn.
type maskedListener struct {
	inner interface {
		Accept() (net.PacketConn, net.Addr, error)
		Close() error
		Addr() net.Addr
	}
	aead cipher.AEAD
}

func (l *maskedListener) Accept() (net.PacketConn, net.Addr, error) {
	pc, addr, err := l.inner.Accept()
	if err != nil {
		return pc, addr, err
	}
	return &maskedConn{PacketConn: pc, aead: l.aead}, addr, nil
}

func (l *maskedListener) Close() error   { return l.inner.Close() }
func (l *maskedListener) Addr() net.Addr { return l.inner.Addr() }

// isClientIDRecord reports whether a client's first DTLS record is Free Turn Proxy's
// greeting rather than tunnel traffic: [length][client id][mode], mode 1 (datagram) or
// 2 (stream), or no mode from older clients. A WireGuard message is at least 32 bytes
// and begins with its type, 1–4, so the two cannot be mistaken for each other.
func isClientIDRecord(b []byte) (ok bool, stream bool) {
	if len(b) == 0 {
		return false, false
	}
	l := int(b[0])
	switch len(b) {
	case 1 + l:
		return true, false
	case 2 + l:
		return true, b[1+l] == 2
	}
	return false, false
}
