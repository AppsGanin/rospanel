package awg

import (
	"fmt"
	"strconv"
	"strings"
)

// Imitation is what AmneziaWG 3.1 sends ahead of a handshake: a short chain of
// decoy datagrams (I1–I5) that look like some other UDP protocol, so the first
// thing a DPI box sees on a new flow is a plausible QUIC, DNS or STUN packet
// rather than an unrecognised blob. They are send-only — the far end never has to
// decode them, and drops them like any other stray datagram — so unlike every
// other parameter here the two ends do not have to agree on them.
//
// The chain is written in the engine's own tag language, which is the ONLY reason
// these are worth generating rather than pasting: a tag can randomise itself on
// every packet. <b 0x…> writes fixed bytes, <r n> n random bytes, <rc n> n random
// letters, <rd n> n random digits and <t> a four-byte unix timestamp. So one
// stored profile still puts a different connection id, a different query name and
// a different payload on the wire every single time.
//
// This is the part the community generators get wrong: they capture one real
// handshake with tshark and paste the bytes, which makes every client on every
// server emit the same 1200 bytes forever — a better signature than the one it
// was hiding. What is fixed here is only what the protocol itself fixes.

// imitation is one protocol's decoy chain.
type imitation struct {
	Name string // for the log and the audit trail, never on the wire
	I1   string
	I2   string
}

// imitations are the profiles a server can be given. All three are protocols that
// are ordinary on UDP, carry opaque payloads (so random bytes are what a real one
// looks like from outside), and are not themselves blocked.
func imitations() []imitation {
	return []imitation{
		{
			// A QUIC v1 Initial from a client: long header with the fixed bit,
			// version 1, an 8-byte destination and source connection id, an empty
			// token, then the length varint and the protected payload. Padded to
			// 1200 bytes, which is what the spec requires of a real Initial and
			// therefore what a short one would stand out against.
			Name: "quic",
			I1: "<b 0xc30000000108><r 8>" + // header, version, DCID length + DCID
				"<b 0x08><r 8>" + // SCID length + SCID
				"<b 0x004496>" + // token length 0, payload length varint 1174
				"<r 1174>", // packet number + protected payload
			// A Handshake packet, which is what really follows an Initial.
			I2: "<b 0xe30000000108><r 8><b 0x08><r 8><b 0x40c8><r 200>",
		},
		{
			// A standard A query for a twelve-letter .com name. The transaction id
			// and the name are redrawn per packet, so two queries never repeat.
			Name: "dns",
			I1: "<r 2>" + // transaction id
				"<b 0x010000010000000000000c>" + // flags, counts, label length 12
				"<rc 12>" + // the name
				"<b 0x03636f6d0000010001>", // ".com", type A, class IN
			// The AAAA query a resolver client sends alongside it.
			I2: "<r 2><b 0x010000010000000000000c><rc 12><b 0x03636f6d00001c0001>",
		},
		{
			// A STUN binding request: type, zero length, the magic cookie every
			// STUN packet carries, and a 96-bit transaction id. Exactly the 20
			// bytes a real one is, which is what any NAT-traversing app sends.
			Name: "stun",
			I1:   "<b 0x000100002112a442><r 12>",
			// The retransmit a client sends when the first goes unanswered, with a
			// timestamp standing in for the changing attributes of a real one.
			I2: "<b 0x000100082112a442><r 12><b 0x00240004><t>",
		},
	}
}

// randomImitation picks one profile. Which one a server gets is itself part of the
// obfuscation: a fleet that all imitates the same protocol is a fleet that can be
// found by looking for that protocol.
func randomImitation() imitation {
	all := imitations()
	return all[randInt(0, len(all)-1)]
}

// The tags the engine understands, and whether each takes a length argument.
// Mirrored from amneziawg-go's obfBuilders — a tag it does not know makes the
// whole chain an error, and the parameters would be refused at apply time rather
// than at save time, which is the wrong end of the operation to find out.
var chainTags = map[string]bool{
	"b": false, "t": false,
	"r": true, "rc": true, "rd": true,
	"d": false, "ds": false, "dz": false,
}

// validateChain refuses a decoy chain the engine would refuse. An empty chain is
// fine and means "send nothing"; what is never fine is an ill-formed one, which
// is how the upstream tools were made to crash.
func validateChain(spec string) error {
	if spec == "" {
		return nil
	}
	rest := spec
	for rest != "" {
		if !strings.HasPrefix(rest, "<") {
			return fmt.Errorf("awg: imitation: %q is outside any tag", rest)
		}
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			return fmt.Errorf("awg: imitation: %q has no closing >", rest)
		}
		parts := strings.Fields(rest[1:end])
		rest = rest[end+1:]
		if len(parts) == 0 {
			return fmt.Errorf("awg: imitation: empty tag in %q", spec)
		}
		needsArg, known := chainTags[parts[0]]
		if !known {
			return fmt.Errorf("awg: imitation: unknown tag <%s>", parts[0])
		}
		switch {
		case parts[0] == "b":
			hex := strings.TrimPrefix(argOf(parts), "0x")
			if hex == "" || len(hex)%2 != 0 || strings.TrimLeft(hex, "0123456789abcdefABCDEF") != "" {
				return fmt.Errorf("awg: imitation: <b> wants an even-length hex string, got %q", argOf(parts))
			}
		case needsArg:
			n, err := strconv.Atoi(argOf(parts))
			if err != nil || n <= 0 || n > maxChainRun {
				return fmt.Errorf("awg: imitation: <%s> wants a length of 1–%d, got %q", parts[0], maxChainRun, argOf(parts))
			}
		}
	}
	return nil
}

func argOf(parts []string) string {
	if len(parts) > 1 {
		return parts[1]
	}
	return ""
}

// maxChainRun bounds one random run. A decoy has to fit in a datagram, and a
// length past this is a typo rather than a plan.
const maxChainRun = 1400
