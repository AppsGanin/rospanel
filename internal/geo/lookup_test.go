package geo

import (
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// --- minimal GeoIPList protobuf encoder, mirroring what geoip.dat carries ---

func lenDelim(tag byte, body []byte) []byte {
	out := binary.AppendUvarint([]byte{tag}, uint64(len(body)))
	return append(out, body...)
}

func cidrMsg(ip []byte, prefix int) []byte {
	body := lenDelim(0x0A, ip)                                      // field 1: ip bytes
	body = binary.AppendUvarint(append(body, 0x10), uint64(prefix)) // field 2: prefix varint
	return body
}

func geoEntry(cc string, cidrs ...[]byte) []byte {
	body := lenDelim(0x0A, []byte(cc)) // field 1: country_code
	for _, c := range cidrs {
		body = append(body, lenDelim(0x12, c)...) // field 2: repeated CIDR
	}
	return body
}

func geoipDat(entries ...[]byte) []byte {
	var out []byte
	for _, e := range entries {
		out = append(out, lenDelim(0x0A, e)...) // GeoIPList: repeated GeoIP (field 1)
	}
	return out
}

func TestCountryLookup(t *testing.T) {
	dir := t.TempDir()
	v6 := netip.MustParseAddr("2a01::").As16()
	data := geoipDat(
		geoEntry("US", cidrMsg([]byte{8, 8, 8, 0}, 24), cidrMsg([]byte{1, 2, 0, 0}, 16)),
		geoEntry("RU", cidrMsg([]byte{5, 45, 0, 0}, 16)),
		geoEntry("private", cidrMsg([]byte{10, 0, 0, 0}, 8)), // >2 chars: must be ignored
		geoEntry("DE", cidrMsg(v6[:], 32)),
	)
	if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, err := LoadCountryLookup(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	cases := []struct {
		ip   string
		want string
		ok   bool
	}{
		{"8.8.8.8", "us", true},    // inside 8.8.8.0/24
		{"8.8.9.1", "", false},     // just outside the /24
		{"1.2.3.4", "us", true},    // inside 1.2.0.0/16
		{"5.45.200.1", "ru", true}, // inside 5.45.0.0/16
		{"10.1.2.3", "", false},    // private/8 was excluded (code not 2 letters)
		{"9.9.9.9", "", false},     // unmapped
		{"2a01::5", "de", true},    // inside 2a01::/32
		{"2a02::1", "", false},     // outside the v6 range
	}
	for _, tc := range cases {
		got, ok := c.Lookup(netip.MustParseAddr(tc.ip))
		if got != tc.want || ok != tc.ok {
			t.Errorf("Lookup(%s) = (%q, %v), want (%q, %v)", tc.ip, got, ok, tc.want, tc.ok)
		}
	}
}

// A corrupt or truncated geoip.dat must fail cleanly, never panic — in particular a
// length prefix ≥ 2^63 (which casts to a negative int) must not slip past the bounds
// check and slice out of range.
func TestCountryLookupMalformed(t *testing.T) {
	dir := t.TempDir()
	// Top-level entry tag 0x0A followed by an absurd length varint.
	bad := binary.AppendUvarint([]byte{0x0A}, uint64(1)<<63)
	if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), bad, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadCountryLookup(dir); err == nil {
		t.Error("expected an error for a malformed geoip.dat, got nil")
	}

	// A truncated valid-looking entry must also not panic.
	trunc := geoipDat(geoEntry("US", cidrMsg([]byte{8, 8, 8, 0}, 24)))
	if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), trunc[:len(trunc)-3], 0o644); err != nil {
		t.Fatalf("write trunc: %v", err)
	}
	_, _ = LoadCountryLookup(dir) // must not panic; result is don't-care
}
