package geo

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
)

// CountryLookup resolves an IP to a 2-letter country code using the CIDR ranges in
// geoip.dat — the very database Xray already routes with, so no extra dependency or
// data file. Country-level only: geoip.dat carries no ASN. Only the 2-letter country
// entries are loaded; service categories (private, cloudflare, telegram, …) that
// overlap countries are skipped, which keeps the country ranges mutually disjoint and
// a binary search correct.
type CountryLookup struct {
	v4 []v4Range // sorted by lo
	v6 []v6Range // sorted by lo
}

type v4Range struct {
	lo, hi uint32
	cc     [2]byte
}

type v6Range struct {
	lo, hi [16]byte
	cc     [2]byte
}

// LoadCountryLookup parses geoip.dat in dir into an IP→country table.
func LoadCountryLookup(dir string) (*CountryLookup, error) {
	data, err := os.ReadFile(filepath.Join(dir, "geoip.dat"))
	if err != nil {
		return nil, err
	}
	c := &CountryLookup{}
	// GeoIPList: a flat sequence of GeoIP entries, each field 1 (tag 0x0A),
	// length-delimited.
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag != 0x0A {
			break
		}
		data = data[n:]
		msgLen, n := binary.Uvarint(data)
		// Unsigned compare: a length varint ≥ 2^63 makes int(msgLen) negative, which
		// would slip past a signed `> len` check and then panic on the slice.
		if n <= 0 || msgLen > uint64(len(data[n:])) {
			break
		}
		data = data[n:]
		c.addEntry(data[:msgLen])
		data = data[msgLen:]
	}
	if len(c.v4) == 0 && len(c.v6) == 0 {
		return nil, fmt.Errorf("geoip.dat in %s has no country ranges", dir)
	}
	sort.Slice(c.v4, func(i, j int) bool { return c.v4[i].lo < c.v4[j].lo })
	sort.Slice(c.v6, func(i, j int) bool { return bytes.Compare(c.v6[i].lo[:], c.v6[j].lo[:]) < 0 })
	return c, nil
}

// addEntry reads one GeoIP entry: field 1 = country_code (string), field 2 = repeated
// CIDR. Only 2-letter country codes are kept.
func (c *CountryLookup) addEntry(msg []byte) {
	var cc [2]byte
	var haveCC bool
	// First pass would need the country before its CIDRs, but the encoder emits the
	// code (field 1) before the CIDRs (field 2), so a single pass works: stash the
	// code, then attach it to each CIDR that follows.
	for len(msg) > 0 {
		field, wire, rest, ok := readTag(msg)
		if !ok {
			return
		}
		msg = rest
		switch {
		case field == 1 && wire == 2: // country_code
			b, rest, ok := readBytes(msg)
			if !ok {
				return
			}
			msg = rest
			if len(b) == 2 {
				cc = [2]byte{lower(b[0]), lower(b[1])}
				haveCC = isCountry(b)
			} else {
				haveCC = false
			}
		case field == 2 && wire == 2: // one CIDR
			b, rest, ok := readBytes(msg)
			if !ok {
				return
			}
			msg = rest
			if haveCC {
				c.addCIDR(cc, b)
			}
		default:
			msg, ok = skipField(msg, wire)
			if !ok {
				return
			}
		}
	}
}

// addCIDR reads a v2ray CIDR message (field 1 = ip bytes, field 2 = prefix varint)
// and appends its range under cc.
func (c *CountryLookup) addCIDR(cc [2]byte, msg []byte) {
	var ip []byte
	var prefix int
	for len(msg) > 0 {
		field, wire, rest, ok := readTag(msg)
		if !ok {
			return
		}
		msg = rest
		switch {
		case field == 1 && wire == 2:
			b, rest, ok := readBytes(msg)
			if !ok {
				return
			}
			ip, msg = b, rest
		case field == 2 && wire == 0:
			v, n := binary.Uvarint(msg)
			if n <= 0 {
				return
			}
			prefix, msg = int(v), msg[n:]
		default:
			msg, ok = skipField(msg, wire)
			if !ok {
				return
			}
		}
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return
	}
	pfx := netip.PrefixFrom(addr, prefix)
	if !pfx.IsValid() {
		return
	}
	pfx = pfx.Masked()
	if addr.Is4() {
		a4 := addr.As4()
		lo := binary.BigEndian.Uint32(a4[:])
		host := uint(32 - pfx.Bits())
		hi := ^uint32(0)
		if host < 32 {
			hi = lo | ((uint32(1) << host) - 1)
		}
		c.v4 = append(c.v4, v4Range{lo: lo, hi: hi, cc: cc})
		return
	}
	lo := pfx.Addr().As16()
	hi := lo
	host := 128 - pfx.Bits()
	for i := 15; i >= 0 && host > 0; i-- {
		n := host
		if n > 8 {
			n = 8
		}
		hi[i] |= byte((1 << uint(n)) - 1)
		host -= n
	}
	c.v6 = append(c.v6, v6Range{lo: lo, hi: hi, cc: cc})
}

// Lookup returns the lowercase 2-letter country code for ip, or ("", false) if no
// country range covers it (a private address, or an IP the database doesn't map).
func (c *CountryLookup) Lookup(ip netip.Addr) (string, bool) {
	if c == nil || !ip.IsValid() {
		return "", false
	}
	ip = ip.Unmap()
	if ip.Is4() {
		a4 := ip.As4()
		v := binary.BigEndian.Uint32(a4[:])
		i := sort.Search(len(c.v4), func(i int) bool { return c.v4[i].lo > v }) - 1
		if i >= 0 && v >= c.v4[i].lo && v <= c.v4[i].hi {
			return string(c.v4[i].cc[:]), true
		}
		return "", false
	}
	b := ip.As16()
	i := sort.Search(len(c.v6), func(i int) bool { return bytes.Compare(c.v6[i].lo[:], b[:]) > 0 }) - 1
	if i >= 0 && bytes.Compare(b[:], c.v6[i].lo[:]) >= 0 && bytes.Compare(b[:], c.v6[i].hi[:]) <= 0 {
		return string(c.v6[i].cc[:]), true
	}
	return "", false
}

// --- tiny protobuf helpers (a fraction of the format, no xray proto dependency) ---

func readTag(msg []byte) (field uint64, wire byte, rest []byte, ok bool) {
	tag, n := binary.Uvarint(msg)
	if n <= 0 {
		return 0, 0, nil, false
	}
	return tag >> 3, byte(tag & 7), msg[n:], true
}

func readBytes(msg []byte) (b, rest []byte, ok bool) {
	l, n := binary.Uvarint(msg)
	// Unsigned compare so a huge (or overflow-to-negative-when-cast) length can't slip
	// past the bound and panic the slice below.
	if n <= 0 || l > uint64(len(msg[n:])) {
		return nil, nil, false
	}
	return msg[n : n+int(l)], msg[n+int(l):], true
}

// skipField advances past a field whose value we don't read, by its wire type.
func skipField(msg []byte, wire byte) ([]byte, bool) {
	switch wire {
	case 0: // varint
		_, n := binary.Uvarint(msg)
		if n <= 0 {
			return nil, false
		}
		return msg[n:], true
	case 1: // 64-bit
		if len(msg) < 8 {
			return nil, false
		}
		return msg[8:], true
	case 2: // length-delimited
		_, rest, ok := readBytes(msg)
		return rest, ok
	case 5: // 32-bit
		if len(msg) < 4 {
			return nil, false
		}
		return msg[4:], true
	default:
		return nil, false
	}
}

// isCountry reports whether the 2-byte code is an ASCII-letter country code. Service
// categories (cloudflare, telegram, private, …) are longer and already excluded by
// the length-2 check at the call site; this rejects any non-letter 2-byte tag.
func isCountry(b []byte) bool {
	if len(b) != 2 {
		return false
	}
	for _, ch := range b {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')) {
			return false
		}
	}
	return true
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
