package model

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// SubCustomApp is an operator-defined "open in app" button on the subscription
// page. Link is the import link with the subscription filled in from a placeholder:
// {url} as is, {url_enc} percent-encoded, {url_b64} base64-encoded.
type SubCustomApp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Platforms string `json:"platforms"`
	Link      string `json:"link"`
}

// SubCustomAppMax caps the operator's own clients: a page button each.
const SubCustomAppMax = 20

// subAppSchemeRe is a URL scheme as RFC 3986 spells it.
var subAppSchemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*$`)

// ValidSubCustomAppLink accepts an import link template: a scheme of its own
// (myapp://…), carrying the subscription through one of the placeholders. Schemes a
// browser would run or read locally are refused — the link lands in an href.
func ValidSubCustomAppLink(link string) bool {
	if !plainLink(link) {
		return false
	}
	if !strings.Contains(link, "{url}") && !strings.Contains(link, "{url_enc}") && !strings.Contains(link, "{url_b64}") {
		return false
	}
	scheme, _, ok := strings.Cut(link, ":")
	if !ok || !subAppSchemeRe.MatchString(scheme) {
		return false
	}
	switch strings.ToLower(scheme) {
	case "javascript", "vbscript", "data", "file", "blob", "about":
		return false
	}
	return true
}

// SubAppLink is one of a client's download buttons.
type SubAppLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// WizardPlatformKeys are the platforms the connect wizard offers, in its order.
var WizardPlatformKeys = []string{"ios", "android", "windows", "macos", "linux", "tv"}

// ValidWizardLink accepts an absolute http(s) download address.
func ValidWizardLink(s string) bool {
	if !plainLink(s) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// plainLink rejects what has no business in a link that lands in an href or a
// script string: anything long, any space or control character (Unicode's bidi
// overrides included), quotes, angle brackets and backslashes.
func plainLink(s string) bool {
	if len(s) > 1000 {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || strings.ContainsRune("\"'<>`\\", r) {
			return false
		}
	}
	return true
}
