// Package strutil contains shared string manipulation and formatting helpers.
package strutil

import (
	"html"
	"strings"
	"unicode/utf8"
)

// EscHTML escapes a string for safe embedding in HTML-parse-mode messages.
func EscHTML(s string) string {
	return html.EscapeString(s)
}

// TruncateName trims leading/trailing whitespace and limits the length to maxRunes,
// ensuring multi-byte Unicode runes are not broken.
func TruncateName(name string, maxRunes int) string {
	name = strings.TrimSpace(name)
	if maxRunes <= 0 || utf8.RuneCountInString(name) <= maxRunes {
		return name
	}
	runes := []rune(name)
	return string(runes[:maxRunes])
}
