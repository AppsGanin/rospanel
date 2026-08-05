// Package i18n localises the text Go renders itself: the Telegram bots and the
// subscription page.
//
// It deliberately does NOT cover the admin panel. Those strings travel to the SPA
// as a key plus arguments and are rendered there, against the same dictionaries
// the rest of the UI uses — so the panel's language is decided in one place, by
// the admin looking at it, rather than negotiated per HTTP request.
//
// The CLI is not covered either: it is English-only by design.
package i18n

import (
	"fmt"
	"strings"
)

// Lang is a supported language tag. Adding a third means adding a catalog file
// next to ru.go/en.go and one case in Normalize.
type Lang string

const (
	RU Lang = "ru"
	EN Lang = "en"
)

// Default is what an unknown or absent preference resolves to. Russian, because
// that is the language the catalogs are written against and the one that is never
// missing a key.
const Default = RU

// Normalize maps an arbitrary tag ("ru-RU", "RU", "en_GB") to a supported Lang.
// Anything that is not recognisably Russian becomes English — a Turkish speaker is
// better served by English than by Russian.
func Normalize(tag string) Lang {
	t := strings.ToLower(strings.TrimSpace(tag))
	switch {
	case t == "":
		return Default
	case strings.HasPrefix(t, "ru"), strings.HasPrefix(t, "be"), strings.HasPrefix(t, "uk"):
		// Belarusian and Ukrainian speakers overwhelmingly read Russian here, and
		// the alternative on offer is English, not their own language.
		return RU
	default:
		return EN
	}
}

// FromAcceptLanguage picks a language from an Accept-Language header. It walks the
// entries in the order the browser listed them and takes the first that resolves
// to something we ship; q-values are not parsed, because browsers already send the
// list in preference order and a hand-written header that disagrees is not worth
// the parser.
//
// A header naming only languages we do not ship falls to Normalize, not to Default:
// stating "de" is not the same as stating nothing. Normalize answers English there,
// which is what the panel's own detector and both bots already do — a German
// speaker reading a Russian subscription page while the bot writes to them in
// English was the disagreement this closes. Default is for a header that says
// nothing at all.
func FromAcceptLanguage(header string) Lang {
	first := ""
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(part)
		if i := strings.IndexByte(tag, ';'); i >= 0 {
			tag = strings.TrimSpace(tag[:i])
		}
		if tag == "" || tag == "*" {
			continue
		}
		if first == "" {
			first = tag
		}
		t := strings.ToLower(tag)
		if strings.HasPrefix(t, "ru") || strings.HasPrefix(t, "be") || strings.HasPrefix(t, "uk") {
			return RU
		}
		if strings.HasPrefix(t, "en") {
			return EN
		}
	}
	if first == "" {
		return Default
	}
	return Normalize(first)
}

// catalogs holds every dictionary, keyed by language. ru is the reference: a key
// missing from another catalog falls back to it (see T), so a half-finished
// translation degrades to Russian instead of rendering a bare key at a user.
var catalogs = map[Lang]map[string]string{
	RU: ru,
	EN: en,
}

// T renders key in lang, substituting args with fmt.Sprintf. A key missing in the
// requested language falls back to Russian; a key missing everywhere returns the
// key itself, which is ugly on screen but traceable in a bug report.
func T(lang Lang, key string, args ...any) string {
	s, ok := catalogs[lang][key]
	if !ok {
		if s, ok = ru[key]; !ok {
			return key
		}
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}
