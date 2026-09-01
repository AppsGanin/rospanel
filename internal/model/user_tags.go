package model

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Limits on the operator's own annotations of a user. Generous for what they are
// for (a line or two, a handful of labels) and small enough that a runaway API
// caller cannot turn the users table into a document store.
const (
	MaxUserNoteLen = 2000 // characters
	MaxUserTags    = 20
	MaxUserTagLen  = 32 // characters
)

// NormalizeTags brings a tag list into canonical form: each tag trimmed and
// lower-cased, blanks dropped, duplicates collapsed, the rest sorted. The result is
// what the store persists and what every comparison runs on, so "VIP", "vip " and
// "vip" are one tag rather than three.
//
// Returns false when a tag cannot be stored: it contains a comma (the on-disk
// separator) or a line break, exceeds MaxUserTagLen, or there are more than
// MaxUserTags of them after normalisation. The caller decides what to tell the
// user; nothing here silently drops a tag someone typed.
func NormalizeTags(in []string) ([]string, bool) {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" {
			continue
		}
		if strings.ContainsAny(t, ",\n\r") || utf8.RuneCountInString(t) > MaxUserTagLen {
			return nil, false
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) > MaxUserTags {
		return nil, false
	}
	sort.Strings(out)
	return out, true
}

// EncodeTags is the on-disk form of a normalised tag list: comma-joined, no
// padding. DecodeTags is its inverse. Both live here rather than in the store so an
// importer or a test can build the column value without going through a database.
func EncodeTags(tags []string) string { return strings.Join(tags, ",") }

// DecodeTags parses what EncodeTags wrote. Tolerant of hand-edited values (blanks,
// spaces, case) since the column is plain text an operator may touch with sqlite3;
// a value that cannot be normalised at all reads as no tags rather than an error,
// because a user row must always load.
func DecodeTags(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	tags, ok := NormalizeTags(strings.Split(s, ","))
	if !ok {
		return []string{}
	}
	return tags
}
