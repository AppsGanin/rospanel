package i18n

import (
	"fmt"
	"strings"
)

//go:generate go run ./gen

// The panel raises errors as a CODE plus a Russian fallback, and the browser renders
// them in the admin's language. The external API has no browser: it used to answer
// with that fallback, so an integration in any language got Russian prose — and the
// code, the one part a caller could branch on, was flattened to a generic
// "bad_request" on the way out.
//
// ErrorEN closes the first half by rendering the panel's own English text (lifted
// from web/src/i18n/en.ts by internal/i18n/gen, so there is no second copy to keep in
// step). The caller publishes the code alongside it for the second.

// ErrorEN returns the English text of an error code with its {{placeholders}} filled
// from args, and reports whether the code is known. An unknown code returns false so
// the caller can keep whatever fallback it already has rather than print an empty
// message.
func ErrorEN(code string, args map[string]any) (string, bool) {
	msg, ok := errEN[code]
	if !ok {
		return "", false
	}
	return interpolate(msg, args), true
}

// interpolate fills {{name}} slots. Unknown slots are left as they are: a visible
// "{{count}}" says "the panel forgot to pass count here", which is the truth and is
// findable — quietly deleting it would leave a sentence that reads fine and lies.
func interpolate(msg string, args map[string]any) string {
	if len(args) == 0 || !strings.Contains(msg, "{{") {
		return msg
	}
	for k, v := range args {
		msg = strings.ReplaceAll(msg, "{{"+k+"}}", fmt.Sprint(v))
	}
	return msg
}

// ErrorCodes lists every code the catalog knows, for the drift test.
func ErrorCodes() []string {
	out := make([]string, 0, len(errEN))
	for k := range errEN {
		out = append(out, k)
	}
	return out
}
