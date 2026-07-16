package geo

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A trimmed slice of the real iplist JSON: keyed by site, each carrying its
// group plus the aggregated CIDRs. Two sites share a group to cover merging.
const globalFixture = `{
  "vk.com":   {"name":"vk.com","group":"VK","domains":["vk.com","vk.ru"],"cidr4":["87.240.128.0/18"],"cidr6":["2a00:bdc0::/32"],"ip4":["87.240.132.72"]},
  "vk.video": {"name":"vk.video","group":"vk","domains":["vkvideo.ru","vk.com"],"cidr4":["93.186.224.0/20","87.240.128.0/18"]},
  "claude.ai":{"name":"claude.ai","group":"ai","domains":["claude.ai"],"cidr4":["104.18.0.0/16"]},
  "orphan":   {"name":"orphan","group":"","domains":["nogroup.example"]}
}`

func writeFixture(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseRef(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantOK  bool
		comment string
	}{
		{"iplist:global/ai", "global/ai", true, "plain ref"},
		{"  iplist:russia/vk  ", "russia/vk", true, "surrounding space"},
		{"geosite:youtube", "", false, "a geosite matcher is not a ref"},
		{"vk.com", "", false, "a bare domain is not a ref"},
		{"iplist:global", "", false, "no group"},
		{"iplist:/ai", "", false, "no source"},
		{"iplist:global/", "", false, "empty group"},
		{"iplist:nosuchsource/ai", "", false, "unknown source"},
	} {
		got, ok := ParseRef(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("ParseRef(%q) = (%q,%v), want (%q,%v) — %s",
				tc.in, got, ok, tc.want, tc.wantOK, tc.comment)
		}
	}
}

func TestGroups(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, ipListGlobal, globalFixture)

	got, err := Groups(dir)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}

	// Group names are lowercased ("VK" and "vk" are one group) and the two sites'
	// rules merge, deduplicated and sorted. ip4 is ignored in favour of cidr4.
	vk, ok := got["global/vk"]
	if !ok {
		t.Fatalf("no global/vk group in %v", got.GroupNames())
	}
	if want := []string{"vk.com", "vk.ru", "vkvideo.ru"}; !slices.Equal(vk.Domains, want) {
		t.Errorf("vk domains = %v, want %v", vk.Domains, want)
	}
	if want := []string{"2a00:bdc0::/32", "87.240.128.0/18", "93.186.224.0/20"}; !slices.Equal(vk.IPs, want) {
		t.Errorf("vk IPs = %v, want %v", vk.IPs, want)
	}
	if slices.Contains(vk.IPs, "87.240.132.72") {
		t.Error("raw ip4 leaked into the rules; cidr4 already covers it")
	}
	// A site with no group is skipped rather than forming an empty-named group.
	if slices.Contains(got.GroupNames(), "global/") {
		t.Errorf("groupless site produced a group: %v", got.GroupNames())
	}
	if want := []string{"global/ai", "global/vk"}; !slices.Equal(got.GroupNames(), want) {
		t.Errorf("GroupNames = %v, want %v", got.GroupNames(), want)
	}
}

// One unusable source must not take the other's groups down with it.
func TestGroupsPartialSources(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, ipListGlobal, globalFixture)
	writeFixture(t, dir, ipListRussia, "{ this is not json")

	got, err := Groups(dir)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if _, ok := got["global/ai"]; !ok {
		t.Errorf("a corrupt russia list dropped global's groups: %v", got.GroupNames())
	}
}

func TestGroupsNoDatabases(t *testing.T) {
	if _, err := Groups(t.TempDir()); err == nil {
		t.Error("Groups on an empty dir: want error, got nil")
	}
}
