package naive

import (
	"encoding/binary"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
)

// NaiveProxy pads the first frames of each tunnel in both directions, so the sizes of a
// TLS handshake inside the tunnel do not show through the outer one. The scheme
// (klzgrad/naiveproxy, src/net/tools/naive/naive_protocol.h, "variant 1"):
//
//	struct PaddedFrame {
//	  uint8 size_high, size_low;  // len(data)
//	  uint8 padding_size;         // 0–255
//	  uint8 data[size];
//	  uint8 zeros[padding_size];
//	};
//
// applied to the first 8 reads and writes; everything after is sent as is.
//
// The client asks for it with "padding-type-request: 1, 0" (preferred first) and the
// server answers with "padding-type-reply". Clients from before that header infer
// padding from a "padding" header in the reply, so one is always sent — it also pads
// the response headers themselves, which is what it was first for.
const (
	paddedFrames = 8
	maxPadding   = 255
	// frameData is the most data one padded frame can carry: the size is 16 bits and
	// the frame, header and padding included, must fit in 65535 bytes.
	frameData = 65535 - 3 - maxPadding
)

// paddingType is what the client asked for: 1 (variant 1) or 0 (none).
func paddingType(h http.Header) (padded bool, reply string) {
	req := h.Get("Padding-Type-Request")
	if req == "" {
		// A client from before negotiation pads whenever it sent a padding header.
		return h.Get("Padding") != "", ""
	}
	for _, t := range strings.Split(req, ",") {
		switch strings.TrimSpace(t) {
		case "1":
			return true, "1"
		case "0":
			return false, "0"
		}
	}
	return false, "0"
}

// nonindexCodes are the printable characters whose HPACK Huffman codes are 8 bits or
// longer, so a header made of them compresses to no less than its length — the same
// alphabet the client and klzgrad/forwardproxy use. The last one fills the tail.
const nonindexCodes = "!#$()+<>?@[]^`{}~"

// paddingHeader is a random-length value for the "padding" response header.
func paddingHeader() string {
	n := 30 + rand.IntN(32)
	b := make([]byte, n)
	bits := rand.Uint64()
	for i := range b {
		if i < 16 {
			b[i] = nonindexCodes[bits&15]
			bits >>= 4
		} else {
			b[i] = nonindexCodes[16]
		}
	}
	return string(b)
}

// unpadReader strips the client's padding from the first frames of the upload.
type unpadReader struct {
	r      io.Reader
	frames int // padded frames started so far
	remain int // data bytes left in the current frame
	pad    int // padding bytes after them
}

func (u *unpadReader) Read(p []byte) (int, error) {
	for u.remain == 0 {
		if u.pad > 0 {
			if _, err := io.CopyN(io.Discard, u.r, int64(u.pad)); err != nil {
				return 0, unexpected(err)
			}
			u.pad = 0
		}
		if u.frames >= paddedFrames {
			return u.r.Read(p)
		}
		var hdr [3]byte
		if _, err := io.ReadFull(u.r, hdr[:]); err != nil {
			if err == io.EOF {
				return 0, io.EOF // the client finished between frames
			}
			return 0, unexpected(err)
		}
		u.frames++
		u.remain = int(binary.BigEndian.Uint16(hdr[:2]))
		u.pad = int(hdr[2])
	}
	if len(p) > u.remain {
		p = p[:u.remain]
	}
	n, err := u.r.Read(p)
	u.remain -= n
	if err == io.EOF && u.remain > 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func unexpected(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// padFrame wraps data, which is at most frameData long, in a padded frame inside buf,
// whose capacity must be at least 3+len(data)+maxPadding, and returns the frame. The
// data must already sit at buf[3:].
func padFrame(buf []byte, n int) []byte {
	pad := rand.IntN(maxPadding + 1)
	binary.BigEndian.PutUint16(buf[:2], uint16(n))
	buf[2] = byte(pad)
	clear(buf[3+n : 3+n+pad])
	return buf[:3+n+pad]
}
