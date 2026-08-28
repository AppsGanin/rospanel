package core

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// --- fixtures: the two tables the panel downloads, in miniature ---

func lenDelim(tag byte, body []byte) []byte {
	out := binary.AppendUvarint([]byte{tag}, uint64(len(body)))
	return append(out, body...)
}

func geoipFixture(t *testing.T, dir string) {
	t.Helper()
	cidr := func(ip []byte, prefix int) []byte {
		body := lenDelim(0x0A, ip)
		return binary.AppendUvarint(append(body, 0x10), uint64(prefix))
	}
	entry := func(cc string, cidrs ...[]byte) []byte {
		body := lenDelim(0x0A, []byte(cc))
		for _, c := range cidrs {
			body = append(body, lenDelim(0x12, c)...)
		}
		return body
	}
	var dat []byte
	// The table spells codes in lower case, exactly as the real one does — that is
	// half of what this test is checking.
	for _, e := range [][]byte{
		entry("nl", cidr([]byte{204, 76, 203, 0}, 24)),
		entry("us", cidr([]byte{20, 246, 0, 0}, 16)),
	} {
		dat = append(dat, lenDelim(0x0A, e)...)
	}
	if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), dat, 0o644); err != nil {
		t.Fatalf("geoip fixture: %v", err)
	}
}

func asnFixture(t *testing.T, dir string) {
	t.Helper()
	tsv := "204.76.203.0\t204.76.203.255\t51396\tNL\tPFCLOUD\n" +
		"20.246.0.0\t20.246.255.255\t8075\tUS\tMICROSOFT-CORP-MSN-AS-BLOCK\n"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(tsv))
	_ = gz.Close()
	if err := os.WriteFile(filepath.Join(dir, "ip2asn.tsv.gz"), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("asn fixture: %v", err)
	}
}

func managerWithGeo(t *testing.T, dir string) *Manager {
	t.Helper()
	return &Manager{sup: xray.NewSupervisor("xray", filepath.Join(dir, "config.json"), dir)}
}

// The whole point of the feature: a bare address becomes an address with a place.
func TestAnnotateProbesFillsCountryAndOperator(t *testing.T) {
	dir := t.TempDir()
	geoipFixture(t, dir)
	asnFixture(t, dir)
	m := managerWithGeo(t, dir)

	probes := []model.ProbeHit{
		{IP: "204.76.203.14"},
		{IP: "20.246.125.85"},
		{IP: "203.0.113.9"}, // in neither table
		{IP: "not-an-ip"},   // the column is text; nothing guarantees it parses
	}
	m.annotateProbes(probes)

	// Upper case, though the table said "nl": the digest puts this next to a flag and
	// the panel next to a country name, and both should read as ISO does.
	if probes[0].Country != "NL" {
		t.Errorf("country = %q, want %q", probes[0].Country, "NL")
	}
	if probes[0].ASN != 51396 || probes[0].Org != "PFCLOUD" {
		t.Errorf("operator = AS%d %q, want AS51396 PFCLOUD", probes[0].ASN, probes[0].Org)
	}
	if probes[1].Country != "US" || probes[1].Org != "MICROSOFT-CORP-MSN-AS-BLOCK" {
		t.Errorf("second probe = %q %q", probes[1].Country, probes[1].Org)
	}
	// An address the tables do not cover keeps its fields empty rather than inventing
	// a place, and a value that is not an address at all must not take the panel down.
	if probes[2].Country != "" || probes[2].Org != "" || probes[2].ASN != 0 {
		t.Errorf("uncovered address was given a place: %+v", probes[2])
	}
	if probes[3].Country != "" || probes[3].Org != "" {
		t.Errorf("unparseable address was given a place: %+v", probes[3])
	}
}

// A panel that has not finished its first geo download still has scanners to show.
func TestAnnotateProbesWithoutTables(t *testing.T) {
	m := managerWithGeo(t, t.TempDir()) // empty dir: no geoip.dat, no ip2asn.tsv.gz
	probes := []model.ProbeHit{{IP: "204.76.203.14", Paths: 10}}
	m.annotateProbes(probes)
	if probes[0].Country != "" || probes[0].Org != "" {
		t.Errorf("fields appeared with no tables loaded: %+v", probes[0])
	}
	if probes[0].Paths != 10 || probes[0].IP != "204.76.203.14" {
		t.Errorf("the row itself was damaged: %+v", probes[0])
	}
	// And with no manager plumbing at all, which is what a fresh boot looks like.
	(&Manager{}).annotateProbes(probes)
}

// The panel's scanner list and the daily digest both go through here. Annotating in
// annotateProbes but forgetting to call it from the reader would leave the feature
// working in tests and absent in the panel.
func TestProbesReturnedToThePanelAreAnnotated(t *testing.T) {
	dir := t.TempDir()
	geoipFixture(t, dir)
	asnFixture(t, dir)
	st, err := store.Open(filepath.Join(dir, "probes.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.RecordProbe("204.76.203.14", 10, time.Now().Unix()); err != nil {
		t.Fatalf("record: %v", err)
	}
	m := &Manager{store: st, sup: xray.NewSupervisor("xray", filepath.Join(dir, "config.json"), dir)}

	probes, err := m.Probes(10)
	if err != nil {
		t.Fatalf("probes: %v", err)
	}
	if len(probes) != 1 {
		t.Fatalf("got %d probes, want 1", len(probes))
	}
	if probes[0].Country != "NL" || probes[0].Org != "PFCLOUD" {
		t.Errorf("the panel would show a bare address: %+v", probes[0])
	}
}
