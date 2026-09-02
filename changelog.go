// Package rospanel carries what belongs to the repository as a whole rather than
// to one of its packages — today, the release history.
package rospanel

import (
	_ "embed"
	"regexp"
	"strings"
	"sync"
)

// CHANGELOG.md is written by release-please on every release; embedding it is what
// lets the panel show "what changed" without a network round-trip to GitHub and
// without a second copy of the text to keep in step.
//
//go:embed CHANGELOG.md
var changelogMD string

// Release is one version's notes: its sections (Features, Bug Fixes, …) with the
// entries under each, links and commit hashes stripped — the reader is an
// operator on the panel, not a reviewer on GitHub.
type Release struct {
	Version  string    `json:"version"`
	Date     string    `json:"date,omitempty"` // YYYY-MM-DD as release-please writes it
	Sections []Section `json:"sections"`
}

// Section is one heading of a release and its bullet points.
type Section struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
}

var (
	changelogOnce sync.Once
	changelog     []Release
)

// Changelog returns the embedded history, newest release first, parsed once.
func Changelog() []Release {
	changelogOnce.Do(func() { changelog = ParseChangelog(changelogMD) })
	return changelog
}

var (
	// "## [2.10.1](compare-url) (2026-08-24)" — the link and the date are optional,
	// since a hand-written entry may carry neither.
	releaseHead = regexp.MustCompile(`^##\s+\[?v?(\d+\.\d+\.\d+[^\]\s)]*)\]?(?:\([^)]*\))?(?:\s+\((\d{4}-\d{2}-\d{2})\))?`)
	sectionHead = regexp.MustCompile(`^###\s+(.+?)\s*$`)
	// "[text](url)" → "text"; a trailing "([abc1234](url))" commit reference is
	// dropped altogether by dropRefs below.
	mdLink   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	refGroup = regexp.MustCompile(`\s*\(\s*[0-9a-f]{7,40}\s*\)\s*$`)
)

// ParseChangelog reads the release-please markdown. Text before the first release
// heading (the "# Changelog" title) is ignored, as is anything under a release
// that is not a bullet in a section.
func ParseChangelog(md string) []Release {
	var out []Release
	var cur *Release
	var sec *Section
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if m := releaseHead.FindStringSubmatch(line); m != nil {
			out = append(out, Release{Version: m[1], Date: m[2]})
			cur = &out[len(out)-1]
			sec = nil
			continue
		}
		if cur == nil {
			continue
		}
		if m := sectionHead.FindStringSubmatch(line); m != nil {
			cur.Sections = append(cur.Sections, Section{Title: m[1]})
			sec = &cur.Sections[len(cur.Sections)-1]
			continue
		}
		if sec == nil {
			continue
		}
		if item, ok := strings.CutPrefix(strings.TrimSpace(line), "* "); ok {
			if text := cleanItem(item); text != "" {
				sec.Items = append(sec.Items, text)
			}
		}
	}
	return out
}

// cleanItem turns a release-please bullet into a sentence: links become their
// text, the commit hash at the end goes, "**scope:**" keeps its scope without the
// emphasis marks.
func cleanItem(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	s = refGroup.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "**", "")
	return strings.TrimSpace(s)
}
