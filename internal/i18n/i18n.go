// Package i18n localises the text Go renders itself: the Telegram bots and the
// subscription page — in Russian, English, Simplified Chinese, Arabic and Persian.
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

// Lang is a supported language tag. Adding one means a catalog file next to
// ru.go, its plural rule, a case in match and a dictionary on the web side.
type Lang string

const (
	RU Lang = "ru"
	EN Lang = "en"
	ZH Lang = "zh"
	AR Lang = "ar"
	FA Lang = "fa"
)

// Langs lists every supported language, Default first.
var Langs = []Lang{RU, EN, ZH, AR, FA}

// IsLang reports whether s is exactly a supported tag — for input that must name
// one rather than be guessed from.
func IsLang(s string) bool {
	_, ok := catalogs[Lang(s)]
	return ok
}

// RTL reports whether lang is written right to left.
func (l Lang) RTL() bool { return l == AR || l == FA }

// Dir is the HTML dir attribute for lang.
func (l Lang) Dir() string {
	if l.RTL() {
		return "rtl"
	}
	return "ltr"
}

// Default is what an unknown or absent preference resolves to. Russian, because
// that is the language the catalogs are written against and the one that is never
// missing a key.
const Default = RU

// Normalize maps an arbitrary tag ("ru-RU", "RU", "en_GB", "zh-Hans") to a
// supported Lang. Anything we do not ship becomes English — a Turkish speaker is
// better served by English than by Russian.
func Normalize(tag string) Lang {
	t := strings.TrimSpace(tag)
	if t == "" {
		return Default
	}
	if l, ok := match(t); ok {
		return l
	}
	return EN
}

// match resolves a tag by its primary subtag.
func match(tag string) (Lang, bool) {
	t := strings.ToLower(tag)
	if i := strings.IndexAny(t, "-_"); i >= 0 {
		t = t[:i]
	}
	switch t {
	case "ru", "be", "uk":
		// Belarusian and Ukrainian speakers overwhelmingly read Russian here, and
		// the alternative on offer is English, not their own language.
		return RU, true
	case "en":
		return EN, true
	case "zh":
		// Traditional script readers too: Simplified is closer than English.
		return ZH, true
	case "ar":
		return AR, true
	case "fa":
		return FA, true
	}
	return "", false
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
		if l, ok := match(tag); ok {
			return l
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
	ZH: zh,
	AR: ar,
	FA: fa,
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

// Every words an interval given in hours as the tail of "at most once every …":
// "an hour", "a day", "a week", or so many days.
func Every(lang Lang, hours int) string {
	switch hours {
	case 1:
		return T(lang, "sub.everyHour")
	case 24:
		return T(lang, "sub.everyDay")
	case 168:
		return T(lang, "sub.everyWeek")
	default:
		return T(lang, "sub.everyDays", (hours+23)/24)
	}
}
