package geo

import (
	"bytes"
	"compress/gzip"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestASNLookup(t *testing.T) {
	dir := t.TempDir()
	tsv := "" +
		"1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET\n" +
		"8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n" +
		"5.0.0.0\t5.255.255.255\t0\tNone\tNot routed\n" + // asn 0 → skipped
		"2a00:1450::\t2a00:1450:ffff:ffff:ffff:ffff:ffff:ffff\t15169\tUS\tGOOGLE\n"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(tsv))
	gz.Close()
	if err := os.WriteFile(filepath.Join(dir, asnFile), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	a, err := LoadASNLookup(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cases := []struct {
		ip   string
		asn  uint32
		org  string
		ok   bool
	}{
		{"1.0.0.5", 13335, "CLOUDFLARENET", true},
		{"8.8.8.8", 15169, "GOOGLE", true},
		{"5.1.2.3", 0, "", false},      // asn 0 range was skipped
		{"9.9.9.9", 0, "", false},      // unmapped
		{"2a00:1450::42", 15169, "GOOGLE", true},
	}
	for _, tc := range cases {
		asn, org, ok := a.Lookup(netip.MustParseAddr(tc.ip))
		if asn != tc.asn || org != tc.org || ok != tc.ok {
			t.Errorf("Lookup(%s) = (%d, %q, %v), want (%d, %q, %v)", tc.ip, asn, org, ok, tc.asn, tc.org, tc.ok)
		}
	}
}
