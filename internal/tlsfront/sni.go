package tlsfront

// The front carries one public port for two TLS listeners: Xray's own certificate for
// the panel's name and every ordinary visitor, and REALITY for the donor name. Which
// one a connection is for is only visible in the ClientHello's server name, so the
// front reads that far and sends the connection accordingly (see Front.Share).

const (
	recordHandshake  = 0x16 // TLS record content type: handshake
	handshakeHello   = 0x01 // handshake message type: ClientHello
	extServerName    = 0x0000
	nameTypeHostName = 0x00
)

// clientHelloSNI names the server name in the ClientHello at the start of b.
//
// needMore reports that b does not hold enough of the ClientHello to tell yet, and the
// caller should read on. When it is false the answer is final: b is not a TLS
// handshake, or the ClientHello is whole and has this SNI — empty when the client sent
// none, which is what a connection to a bare address looks like.
func clientHelloSNI(b []byte) (sni string, needMore bool) {
	if len(b) == 0 {
		return "", true
	}
	if b[0] != recordHandshake {
		return "", false
	}
	// A ClientHello may be split over several handshake records (a big post-quantum
	// key share does this), so the records are joined before the message is parsed.
	var hs []byte
	for pos := 0; ; {
		if pos+5 > len(b) {
			return "", true
		}
		if b[pos] != recordHandshake {
			return "", false // not a handshake record: no ClientHello here
		}
		n := int(b[pos+3])<<8 | int(b[pos+4])
		if n == 0 || pos+5+n > len(b) {
			return "", true // the record is still arriving
		}
		hs = append(hs, b[pos+5:pos+5+n]...)
		pos += 5 + n
		if len(hs) < 4 {
			continue
		}
		if hs[0] != handshakeHello {
			return "", false
		}
		msgLen := int(hs[1])<<16 | int(hs[2])<<8 | int(hs[3])
		// version(2) + random(32) is the least a ClientHello body can be.
		if msgLen < 34 || msgLen > 1<<16 {
			return "", false
		}
		if len(hs) < 4+msgLen {
			if pos >= len(b) {
				return "", true
			}
			continue
		}
		return clientHelloName(hs[4 : 4+msgLen]), false
	}
}

// clientHelloName reads the server name out of a whole ClientHello body — everything
// after the handshake header: version, random, session id, ciphers, compression and the
// extension list. Anything malformed reads as "no name", which sends the connection
// the way every other one went before the split existed.
func clientHelloName(b []byte) string {
	const fixed = 2 + 32 // version + random
	p := fixed
	if p >= len(b) {
		return ""
	}
	p += 1 + int(b[p]) // session id
	if p+2 > len(b) {
		return ""
	}
	p += 2 + (int(b[p])<<8 | int(b[p+1])) // cipher suites
	if p >= len(b) {
		return ""
	}
	p += 1 + int(b[p]) // compression methods
	if p+2 > len(b) {
		return ""
	}
	end := p + 2 + (int(b[p])<<8 | int(b[p+1])) // extensions
	if end > len(b) {
		end = len(b)
	}
	for p += 2; p+4 <= end; {
		typ := int(b[p])<<8 | int(b[p+1])
		l := int(b[p+2])<<8 | int(b[p+3])
		p += 4
		if p+l > end {
			return ""
		}
		if typ == extServerName {
			return serverNameHost(b[p : p+l])
		}
		p += l
	}
	return ""
}

// serverNameHost reads the host_name out of the server_name extension, whose list can
// also carry no name type a server understands (only host_name is ever sent).
func serverNameHost(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	end := 2 + (int(b[0])<<8 | int(b[1]))
	if end > len(b) {
		end = len(b)
	}
	for p := 2; p+3 <= end; {
		typ := b[p]
		l := int(b[p+1])<<8 | int(b[p+2])
		p += 3
		if p+l > end {
			return ""
		}
		if typ == nameTypeHostName {
			return string(b[p : p+l])
		}
		p += l
	}
	return ""
}
