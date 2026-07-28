package i18n

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestCatalogsMatch is the Go-side equivalent of the frontend's typed dictionaries:
// there is no compiler to catch a key that exists in one language and not the
// other, so the test does it instead.
// Plural keys are deliberately exempt: which forms a language needs is decided by
// its own grammar, so English carries _one/_other where Russian carries
// _one/_few/_many. Their completeness is TestPluralFormsComplete's job; what this
// test enforces for them is that the BASE exists on both sides.
func TestCatalogsMatch(t *testing.T) {
	baseOf := func(k string) string {
		if b, ok := pluralBase(k); ok {
			return b
		}
		return k
	}
	bases := func(cat map[string]string) map[string]bool {
		out := map[string]bool{}
		for k := range cat {
			out[baseOf(k)] = true
		}
		return out
	}
	for lang := range catalogs {
		if lang == RU {
			continue
		}
		var missing, extra []string
		ruBases, langBases := bases(ru), bases(catalogs[lang])
		for b := range ruBases {
			if !langBases[b] {
				missing = append(missing, b)
			}
		}
		for b := range langBases {
			if !ruBases[b] {
				extra = append(extra, b)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 {
			t.Errorf("%s: %d keys missing from the catalog: %v", lang, len(missing), missing)
		}
		if len(extra) > 0 {
			t.Errorf("%s: %d keys not in the reference catalog: %v", lang, len(extra), extra)
		}
	}
}

var verbRe = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z]`)

// formatVerbs lists the Sprintf verbs in s. "%%" is an escaped percent sign, not a
// verb, and must be removed FIRST: left in place, the second % pairs with whatever
// follows it ("%% used" scanned as the verb "% u"), which reports a mismatch
// between two translations that are actually fine.
func formatVerbs(s string) []string {
	return verbRe.FindAllString(strings.ReplaceAll(s, "%%", ""), -1)
}

// TestFormatVerbsMatch guards the other half of a translation's contract: T passes
// the same arguments whatever the language, so every catalog must use the same
// format verbs in the same order. A translation that drops a %s renders "%!s(MISSING)"
// at a user; one that reorders %d and %s renders nonsense.
func TestFormatVerbsMatch(t *testing.T) {
	for lang, cat := range catalogs {
		if lang == RU {
			continue
		}
		for k, want := range ru {
			got, ok := cat[k]
			if !ok {
				continue // a plural form this language does not use, or already reported
			}
			wv, gv := formatVerbs(want), formatVerbs(got)
			if len(wv) != len(gv) {
				t.Errorf("%s/%s: %d format verbs, ru has %d", lang, k, len(gv), len(wv))
				continue
			}
			for i := range wv {
				if wv[i] != gv[i] {
					t.Errorf("%s/%s: verb %d is %q, ru has %q", lang, k, i, gv[i], wv[i])
				}
			}
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]Lang{
		"":      Default,
		"ru":    RU,
		"ru-RU": RU,
		"RU":    RU,
		"uk":    RU,
		"be-BY": RU,
		"en":    EN,
		"en-GB": EN,
		"de":    EN,
		"zh-CN": EN,
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// The header decides the subscription page's language, and it has to answer the
// same way Normalize does for a language we do not ship — the panel's detector and
// both bots send those readers to English, and a page that sent them to Russian
// instead was a live disagreement between surfaces.
func TestFromAcceptLanguage(t *testing.T) {
	cases := map[string]Lang{
		"":                           Default,
		"*":                          Default,
		"ru-RU,ru;q=0.9,en-US;q=0.8": RU,
		"en-US,en;q=0.9":             EN,
		"de-DE,de;q=0.9,en;q=0.8":    EN,
		"fr;q=0.9, ru;q=0.8":         RU, // first *supported* entry wins
		"zh-CN,zh;q=0.9":             EN, // named a language, just not one we ship
		"de-DE,de;q=0.9":             EN, // ditto — must agree with the bots, which say EN
		"be-BY,be":                   RU, // Normalize's special cases still hold
		"uk":                         RU,
		"  en-GB  ,  ru  ":           EN,
	}
	for in, want := range cases {
		if got := FromAcceptLanguage(in); got != want {
			t.Errorf("FromAcceptLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTFallsBackToReference(t *testing.T) {
	if got := T(EN, "sub.copied"); got != "Copied" {
		t.Errorf("T(en, sub.copied) = %q", got)
	}
	if got := T(Lang("de"), "sub.copied"); got != ru["sub.copied"] {
		t.Errorf("unknown language should fall back to ru, got %q", got)
	}
	if got := T(EN, "no.such.key"); got != "no.such.key" {
		t.Errorf("missing key should render as itself, got %q", got)
	}
	if got := T(EN, "sub.until", "2026-08-07"); got != "until 2026-08-07" {
		t.Errorf("T with args = %q", got)
	}
}

// TestPluralFormsComplete is the Go twin of the frontend's compile-time plural
// guard. There the type system enforces it; here nothing does, so the test must —
// and the bug it prevents is not hypothetical: the panel's English dictionary once
// carried Russian-style _one/_few/_many and no _other, so every English count of
// two or more silently rendered Russian.
func TestPluralFormsComplete(t *testing.T) {
	bases := map[string]bool{}
	for k := range ru {
		if base, ok := pluralBase(k); ok {
			bases[base] = true
		}
	}
	// Counts that between them select every category either language can produce.
	probes := []int{0, 1, 2, 5, 11, 21, 22, 25, 101, 111}
	for lang := range catalogs {
		for base := range bases {
			for _, n := range probes {
				key := base + "_" + plural(lang, n)
				if _, ok := catalogs[lang][key]; !ok {
					t.Errorf("%s: %q missing (selected for count=%d)", lang, key, n)
					break
				}
			}
		}
	}
}

func TestPluralCategories(t *testing.T) {
	ruCases := map[int]string{
		1: "one", 21: "one", 101: "one",
		2: "few", 3: "few", 4: "few", 22: "few",
		0: "many", 5: "many", 11: "many", 12: "many", 14: "many", 25: "many",
	}
	for n, want := range ruCases {
		if got := plural(RU, n); got != want {
			t.Errorf("plural(ru, %d) = %q, want %q", n, got, want)
		}
	}
	for n, want := range map[int]string{1: "one", 0: "other", 2: "other", 21: "other"} {
		if got := plural(EN, n); got != want {
			t.Errorf("plural(en, %d) = %q, want %q", n, got, want)
		}
	}
}

func TestTNRendersCount(t *testing.T) {
	for _, tc := range []struct {
		lang Lang
		n    int
		want string
	}{
		{RU, 1, "1 день"},
		{RU, 3, "3 дня"},
		{RU, 11, "11 дней"},
		{EN, 1, "1 day"},
		{EN, 3, "3 days"},
	} {
		if got := TN(tc.lang, "notify.days", tc.n); got != tc.want {
			t.Errorf("TN(%s, notify.days, %d) = %q, want %q", tc.lang, tc.n, got, tc.want)
		}
	}
}

// The three language sources must agree on what a given language means: the bots
// resolve a Telegram language_code through Normalize, the subscription page
// resolves an Accept-Language header, and the panel's own detector reads
// navigator.languages. If they disagree, one person gets English in the bot and
// Russian on the page — which is exactly what happened for German.
func TestAcceptLanguageAgreesWithNormalize(t *testing.T) {
	for _, tag := range []string{
		"ru", "ru-RU", "be", "be-BY", "uk", "uk-UA", // Russian, and the two Normalize folds in
		"en", "en-US", "en-GB",
		"de", "de-DE", "fr", "zh-CN", "tr", "es-MX", "ar", // ship nothing for these
	} {
		if got, want := FromAcceptLanguage(tag), Normalize(tag); got != want {
			t.Errorf("FromAcceptLanguage(%q) = %q but Normalize(%q) = %q — the page and the bots disagree",
				tag, got, tag, want)
		}
	}
	// An absent header states nothing, so it is the one case that falls to Default
	// rather than to Normalize's English.
	if got := FromAcceptLanguage(""); got != Default {
		t.Errorf("FromAcceptLanguage(\"\") = %q, want %q", got, Default)
	}
}
