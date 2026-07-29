package i18n

// Plural categories, named after CLDR so the suffixes match the frontend's
// dictionaries (which get theirs from Intl.PluralRules).
const (
	catOne   = "one"
	catFew   = "few"
	catMany  = "many"
	catOther = "other"
)

// plural returns the CLDR plural category for n in lang.
//
// Only integer counts are covered, which is all this codebase has: days left,
// devices, orders. English distinguishes one from everything else; Russian needs
// three forms, and getting the 11–14 exception wrong is the classic bug ("11 дня").
func plural(lang Lang, n int) string {
	if lang == EN {
		if n == 1 {
			return catOne
		}
		return catOther
	}
	// Russian (and the languages Normalize folds into it).
	if n < 0 {
		n = -n
	}
	mod10, mod100 := n%10, n%100
	switch {
	case mod10 == 1 && mod100 != 11:
		return catOne
	case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
		return catFew
	default:
		return catMany
	}
}

// TN renders a count-dependent key. The catalog holds one entry per plural form,
// suffixed "<key>_one" / "_few" / "_many" / "_other", and n is passed to Sprintf as
// the first argument, ahead of args.
//
// A language whose selected form is missing falls back to the reference catalog,
// exactly like T — and TestPluralFormsComplete makes that impossible to ship.
func TN(lang Lang, key string, n int, args ...any) string {
	full := key + "_" + plural(lang, n)
	if _, ok := catalogs[lang][full]; !ok {
		// Fall back within the same language before crossing into another one: an
		// English catalog that only defines _other should still answer for n == 1
		// in English rather than switching the user to Russian mid-sentence.
		if _, ok := catalogs[lang][key+"_"+catOther]; ok {
			full = key + "_" + catOther
		}
	}
	return T(lang, full, append([]any{n}, args...)...)
}

// pluralBase strips a known plural suffix, or reports false. Used by the tests.
func pluralBase(key string) (string, bool) {
	for _, suf := range []string{"_" + catOne, "_" + catFew, "_" + catMany, "_" + catOther} {
		if len(key) > len(suf) && key[len(key)-len(suf):] == suf {
			return key[:len(key)-len(suf)], true
		}
	}
	return "", false
}
