package geo

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// maxASNDecompressed bounds how many bytes we inflate from the gzip so a
	// decompression bomb (or a hostile upstream past the on-disk size cap) can't pin
	// CPU/memory. Well above the real ip2asn-combined TSV (tens of MB) and comfortably
	// above what maxASNRows can produce, so the row cap is the one that normally binds
	// (it errors cleanly) and this only ever catches a genuine bomb.
	maxASNDecompressed = 512 << 20
	// maxASNRows bounds the number of lines parsed, capping resident memory (the range
	// slices) regardless of how many rows the stream carries. Several times the real
	// table size (~500k rows today).
	maxASNRows = 4_000_000
)

// ASNLookup resolves an IP to its ASN (autonomous system number) and the operator's
// name, from iptoasn.com's free gzipped TSV. The panel uses it to label where
// connections come from by provider, not just country. Ranges are explicit start/end
// pairs (a routing table), so they're disjoint and binary-searchable; the AS
// description is interned by number to avoid storing it on every range.
type ASNLookup struct {
	v4  []asnRange4
	v6  []asnRange6
	org map[uint32]string
}

type asnRange4 struct {
	lo, hi, asn uint32
}

type asnRange6 struct {
	lo, hi [16]byte
	asn    uint32
}

// LoadASNLookup parses ip2asn.tsv.gz in dir.
func LoadASNLookup(dir string) (*ASNLookup, error) {
	f, err := os.Open(filepath.Join(dir, asnFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	a := &ASNLookup{org: map[uint32]string{}}
	sc := bufio.NewScanner(io.LimitReader(gz, maxASNDecompressed))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	rows := 0
	for sc.Scan() {
		if rows++; rows > maxASNRows {
			return nil, fmt.Errorf("%s in %s exceeds %d rows", asnFile, dir, maxASNRows)
		}
		// range_start \t range_end \t AS_number \t country \t AS_description
		cols := strings.Split(sc.Text(), "\t")
		if len(cols) < 5 {
			continue
		}
		n, err := strconv.ParseUint(cols[2], 10, 32)
		if err != nil || n == 0 { // 0 = not routed to any AS
			continue
		}
		asn := uint32(n)
		lo, err1 := netip.ParseAddr(cols[0])
		hi, err2 := netip.ParseAddr(cols[1])
		if err1 != nil || err2 != nil || lo.Is4() != hi.Is4() {
			continue
		}
		if _, seen := a.org[asn]; !seen {
			a.org[asn] = strings.TrimSpace(cols[4])
		}
		if lo.Is4() {
			l4, h4 := lo.As4(), hi.As4()
			a.v4 = append(a.v4, asnRange4{
				lo:  binary.BigEndian.Uint32(l4[:]),
				hi:  binary.BigEndian.Uint32(h4[:]),
				asn: asn,
			})
		} else {
			a.v6 = append(a.v6, asnRange6{lo: lo.As16(), hi: hi.As16(), asn: asn})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// The scanner stops at EOF, and io.LimitReader fakes EOF at maxASNDecompressed. If
	// the gzip stream still has bytes past that cap, we truncated it mid-file — refuse
	// rather than silently install a partial table as if it were the whole thing (the
	// row cap above errors cleanly; the byte cap must too, not swallow the tail).
	if n, _ := gz.Read(make([]byte, 1)); n > 0 {
		return nil, fmt.Errorf("%s in %s exceeds %d decompressed bytes", asnFile, dir, maxASNDecompressed)
	}
	if len(a.v4) == 0 && len(a.v6) == 0 {
		return nil, fmt.Errorf("%s in %s has no ASN ranges", asnFile, dir)
	}
	sort.Slice(a.v4, func(i, j int) bool { return a.v4[i].lo < a.v4[j].lo })
	sort.Slice(a.v6, func(i, j int) bool { return bytes.Compare(a.v6[i].lo[:], a.v6[j].lo[:]) < 0 })
	return a, nil
}

// Lookup returns the ASN and operator name covering ip, or (0, "", false) if none
// does (a private or unrouted address).
func (a *ASNLookup) Lookup(ip netip.Addr) (uint32, string, bool) {
	if a == nil || !ip.IsValid() {
		return 0, "", false
	}
	ip = ip.Unmap()
	if ip.Is4() {
		b := ip.As4()
		v := binary.BigEndian.Uint32(b[:])
		i := sort.Search(len(a.v4), func(i int) bool { return a.v4[i].lo > v }) - 1
		if i >= 0 && v >= a.v4[i].lo && v <= a.v4[i].hi {
			return a.v4[i].asn, a.org[a.v4[i].asn], true
		}
		return 0, "", false
	}
	b := ip.As16()
	i := sort.Search(len(a.v6), func(i int) bool { return bytes.Compare(a.v6[i].lo[:], b[:]) > 0 }) - 1
	if i >= 0 && bytes.Compare(b[:], a.v6[i].lo[:]) >= 0 && bytes.Compare(b[:], a.v6[i].hi[:]) <= 0 {
		return a.v6[i].asn, a.org[a.v6[i].asn], true
	}
	return 0, "", false
}
