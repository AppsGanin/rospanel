package awg

import (
	"strings"
	"testing"
)

// Every profile has to be a chain the engine parses AND a packet shaped like the
// protocol it claims to be. The second half is what a decoy is for: a QUIC
// Initial that is 200 bytes, or a DNS query with the wrong counts, is a worse
// signature than sending nothing.
func TestImitationProfilesAreWellFormedAndTheRightSize(t *testing.T) {
	want := map[string][2]int{
		// A QUIC Initial must be padded to 1200 bytes. The Handshake packet that
		// follows declares 200 bytes of payload and carries exactly that.
		"quic": {1200, 225},
		// A query for a 12-letter .com name, twice.
		"dns": {34, 34},
		// A STUN binding request is exactly its 20-byte header. The retransmit
		// declares 8 bytes of attributes and carries one: a 4-byte header and a
		// 4-byte value.
		"stun": {20, 28},
	}
	for _, im := range imitations() {
		if err := validateChain(im.I1); err != nil {
			t.Errorf("%s i1: %v", im.Name, err)
		}
		if err := validateChain(im.I2); err != nil {
			t.Errorf("%s i2: %v", im.Name, err)
		}
		sizes, ok := want[im.Name]
		if !ok {
			t.Errorf("profile %q has no expected size — add one", im.Name)
			continue
		}
		if got := chainLen(t, im.I1); got != sizes[0] {
			t.Errorf("%s i1 is %d bytes, want %d", im.Name, got, sizes[0])
		}
		if got := chainLen(t, im.I2); got != sizes[1] {
			t.Errorf("%s i2 is %d bytes, want %d", im.Name, got, sizes[1])
		}
		// A decoy whose bytes never change is a signature of its own. Every
		// profile must carry at least one tag that redraws per packet.
		if !strings.Contains(im.I1, "<r ") && !strings.Contains(im.I1, "<rc ") &&
			!strings.Contains(im.I1, "<rd ") && !strings.Contains(im.I1, "<t>") {
			t.Errorf("%s i1 is the same bytes every time", im.Name)
		}
	}
}

// The generator has to reach every profile — one that can never be drawn is dead
// code pretending to be variety.
func TestEveryImitationProfileGetsPicked(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		seen[randomImitation().Name] = true
	}
	for _, im := range imitations() {
		if !seen[im.Name] {
			t.Errorf("profile %q is never picked", im.Name)
		}
	}
}

func TestValidateChainRefusesWhatTheEngineWould(t *testing.T) {
	for _, bad := range []string{
		"<b>",              // no bytes
		"<b 0x0>",          // odd hex
		"<b 0xzz>",         // not hex
		"<zz 4>",           // unknown tag
		"<r 0>",            // an empty run
		"<r 99999>",        // past a datagram
		"<r>",              // no length
		"<b 0x00",          // unclosed
		"junk<b 0x00>",     // text outside a tag
		"<b 0x00>trailing", // and after one
	} {
		if err := validateChain(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if err := validateChain(""); err != nil {
		t.Errorf("an absent chain is not an error: %v", err)
	}
}

// chainLen is how many bytes on the wire a chain produces.
func chainLen(t *testing.T, spec string) int {
	t.Helper()
	total, rest := 0, spec
	for rest != "" {
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			t.Fatalf("unclosed tag in %q", spec)
		}
		parts := strings.Fields(rest[1:end])
		rest = rest[end+1:]
		switch parts[0] {
		case "b":
			total += (len(strings.TrimPrefix(parts[1], "0x")) / 2)
		case "t":
			total += 4
		case "r", "rc", "rd":
			n := 0
			for _, c := range parts[1] {
				n = n*10 + int(c-'0')
			}
			total += n
		default:
			t.Fatalf("unhandled tag <%s>", parts[0])
		}
	}
	return total
}

// The size and shape tests above compare a profile against itself: change the
// profile and they change with it, so a chain that stopped looking like the
// protocol it names would sail through them. These are the bytes the protocols
// themselves define, written out here independently. If a profile stops matching
// one, either the profile is wrong or it is imitating something else now.
func TestProfilesMatchTheProtocolsTheyName(t *testing.T) {
	byName := map[string]imitation{}
	for _, im := range imitations() {
		byName[im.Name] = im
	}

	// RFC 5389: a STUN message begins with a 14-bit type — 0x0001 is a binding
	// request — a 16-bit length, and the magic cookie 0x2112A442, then 96 bits of
	// transaction id.
	stun := byName["stun"]
	if !strings.HasPrefix(stun.I1, "<b 0x000100002112a442>") {
		t.Errorf("stun i1 is not a binding request with the magic cookie: %s", stun.I1)
	}
	if !strings.Contains(stun.I2, "2112a442") {
		t.Errorf("stun i2 has lost the magic cookie: %s", stun.I2)
	}

	// RFC 9000: a long-header packet has the high bit set and the fixed bit after
	// it (0xc0 masked), and version 1 is 0x00000001. An Initial must be padded to
	// at least 1200 bytes by the client.
	quic := byName["quic"]
	if !strings.HasPrefix(quic.I1, "<b 0xc3") {
		t.Errorf("quic i1 is not a long-header packet: %s", quic.I1)
	}
	if !strings.Contains(quic.I1, "00000001") {
		t.Errorf("quic i1 does not carry version 1: %s", quic.I1)
	}
	if n := chainLen(t, quic.I1); n < 1200 {
		t.Errorf("a QUIC Initial must be padded to 1200 bytes, this one is %d", n)
	}

	// RFC 1035: a query carries flags 0x0100 (recursion desired), exactly one
	// question and no answers, and ends with QTYPE and QCLASS. 0x0001 is an A
	// record, 0x001c an AAAA, and 0x0001 the IN class.
	dns := byName["dns"]
	if !strings.Contains(dns.I1, "0x0100000100000000") {
		t.Errorf("dns i1 is not one question with recursion desired: %s", dns.I1)
	}
	if !strings.HasSuffix(dns.I1, "0000010001>") {
		t.Errorf("dns i1 does not end in QTYPE A, QCLASS IN: %s", dns.I1)
	}
	if !strings.HasSuffix(dns.I2, "00001c0001>") {
		t.Errorf("dns i2 does not end in QTYPE AAAA, QCLASS IN: %s", dns.I2)
	}
	// The label length byte must equal the run of letters that follows it.
	if !strings.Contains(dns.I1, "0c><rc 12>") {
		t.Errorf("dns i1's label length does not match its name: %s", dns.I1)
	}
}
