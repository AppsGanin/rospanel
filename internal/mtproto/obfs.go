package mtproto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"slices"
)

// Obfuscated2 (https://core.telegram.org/mtproto/mtproto-transports#transport-obfuscation):
// the stream opens with 64 bytes — 8 of noise, a 32-byte AES key, a 16-byte IV, the
// transport's tag and the DC — and is AES-256-CTR from there. With a secret the key is
// SHA-256(key ‖ secret). The other direction uses the same bytes reversed.

const (
	frameLen  = 64
	offKey    = 8
	offIV     = offKey + 32
	offTag    = offIV + 16
	offDC     = offTag + 4
	defaultDC = 2
)

// tagPadded is the padded intermediate transport, the one fake TLS carries.
var tagPadded = [4]byte{0xdd, 0xdd, 0xdd, 0xdd}

type frame [frameLen]byte

// ctr is the stream cipher of one direction, from the frame's key and IV (key hashed
// with secret when there is one).
func (f *frame) ctr(secret []byte) cipher.Stream {
	key := f[offKey:offIV]
	if secret != nil {
		h := sha256.New()
		h.Write(key)
		h.Write(secret)
		key = h.Sum(nil)
	}
	block, _ := aes.NewCipher(key)
	return cipher.NewCTR(block, f[offIV:offTag])
}

// reversed is the frame with its key and IV bytes in reverse order: the other direction.
func (f *frame) reversed() *frame {
	r := *f
	slices.Reverse(r[offKey:offTag])
	return &r
}

// readClientFrame reads the client's opening frame and returns the DC it asks for and the
// ciphers of the two directions as the server sees them: recv decrypts what the client
// sends, send encrypts what goes back.
func readClientFrame(r io.Reader, secret []byte) (dc int, recv, send cipher.Stream, err error) {
	var f frame
	if _, err := io.ReadFull(r, f[:]); err != nil {
		return 0, nil, nil, err
	}
	recv = f.ctr(secret)
	send = f.reversed().ctr(secret)
	recv.XORKeyStream(f[:], f[:])
	if subtle.ConstantTimeCompare(f[offTag:offDC], tagPadded[:]) != 1 {
		return 0, nil, nil, errors.New("mtproto: unsupported transport")
	}
	dc = int(int16(binary.LittleEndian.Uint16(f[offDC:])))
	return dc, recv, send, nil
}

// openFrame makes the frame a connection is opened with — the proxy's to Telegram has
// no secret — and the ciphers of that connection: send encrypts what goes out, recv
// decrypts what comes back. The frame is ready to write.
func openFrame(dc int, secret []byte) (out []byte, send, recv cipher.Stream) {
	var f frame
	for {
		_, _ = rand.Read(f[:])
		// What tdlib refuses to open with: a first byte that reads as the abridged
		// transport, and openings that read as HTTP or another transport's tag.
		if f[0] == 0xef {
			continue
		}
		switch binary.LittleEndian.Uint32(f[:4]) {
		case 0x44414548, 0x54534f50, 0x20544547, 0x4954504f, 0x02010316, 0xdddddddd, 0xeeeeeeee:
			continue
		}
		if f[4]|f[5]|f[6]|f[7] == 0 {
			continue
		}
		break
	}
	copy(f[offTag:], tagPadded[:])
	binary.LittleEndian.PutUint16(f[offDC:], uint16(int16(dc)))
	send = f.ctr(secret)
	recv = f.reversed().ctr(secret)
	enc := f
	send.XORKeyStream(enc[:], enc[:])
	// Only the tag and the DC go encrypted; the key and IV are what the other end
	// derives its ciphers from.
	copy(enc[:offTag], f[:offTag])
	return enc[:], send, recv
}

// Telegram's core data centres (tdesktop's built-in list), DC 203 its CDN. IPv4 first:
// a server without IPv6 fails over to the next address only after a timeout.
var dcAddrs = map[int][]string{
	1:   {"149.154.175.50:443", "[2001:b28:f23d:f001::a]:443"},
	2:   {"149.154.167.51:443", "95.161.76.100:443", "[2001:67c:4e8:f002::a]:443"},
	3:   {"149.154.175.100:443", "[2001:b28:f23d:f003::a]:443"},
	4:   {"149.154.167.91:443", "[2001:67c:4e8:f004::a]:443"},
	5:   {"149.154.171.5:443", "[2001:b28:f23f:f005::a]:443"},
	203: {"91.105.192.100:443", "[2a0a:f280:203:a:5000::100]:443"},
}

// dcAddresses are the addresses of DC dc (a media DC by its negative number) and the
// DC they are — DC 2's, and 2, for one Telegram does not list (0, a test DC).
func dcAddresses(dc int) ([]string, int) {
	if dc < 0 {
		dc = -dc
	}
	if a, ok := dcAddrs[dc]; ok {
		return a, dc
	}
	return dcAddrs[defaultDC], defaultDC
}
