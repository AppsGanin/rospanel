package rospanel

import "testing"

const sample = `# Changelog

## [2.10.1](https://github.com/x/y/compare/v2.10.0...v2.10.1) (2026-08-24)


### Bug Fixes

* count devices by HWID when HWID is what identifies them ([#66](https://github.com/x/y/issues/66)) ([38a51ea](https://github.com/x/y/commit/38a51ea))
* **billing:** refactor free plan logic and upgrade Xray ([4d031a3](https://github.com/x/y/commit/4d031a3))


### Reverts

* drop the handover grace ([33f8745](https://github.com/x/y/commit/33f8745))

## 2.10.0 (2026-08-22)

### Features

* AmneziaWG as a built-in lane ([94af079](https://github.com/x/y/commit/94af079))
stray text that is not a bullet
`

func TestParseChangelog(t *testing.T) {
	rel := ParseChangelog(sample)
	if len(rel) != 2 {
		t.Fatalf("releases: %d", len(rel))
	}
	if rel[0].Version != "2.10.1" || rel[0].Date != "2026-08-24" || rel[1].Version != "2.10.0" {
		t.Fatalf("heads: %+v %+v", rel[0], rel[1])
	}
	if len(rel[0].Sections) != 2 || rel[0].Sections[0].Title != "Bug Fixes" || rel[0].Sections[1].Title != "Reverts" {
		t.Fatalf("sections: %+v", rel[0].Sections)
	}
	items := rel[0].Sections[0].Items
	if len(items) != 2 {
		t.Fatalf("items: %v", items)
	}
	if items[0] != "count devices by HWID when HWID is what identifies them (#66)" {
		t.Errorf("links not flattened / hash not dropped: %q", items[0])
	}
	if items[1] != "billing: refactor free plan logic and upgrade Xray" {
		t.Errorf("scope emphasis kept: %q", items[1])
	}
	if got := rel[1].Sections[0].Items; len(got) != 1 || got[0] != "AmneziaWG as a built-in lane" {
		t.Errorf("second release: %v", got)
	}
}

// The embedded file is the real one: it must parse to something, and its newest
// entry must look like a version, or the panel would show an empty "what's new".
func TestEmbeddedChangelogParses(t *testing.T) {
	rel := Changelog()
	if len(rel) == 0 {
		t.Fatal("no releases parsed from CHANGELOG.md")
	}
	if rel[0].Version == "" || len(rel[0].Sections) == 0 {
		t.Fatalf("newest release is empty: %+v", rel[0])
	}
}
