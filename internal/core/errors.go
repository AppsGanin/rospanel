package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Shu1t3/rospanel-shu1t3/internal/model"
)

// ValidationError marks an error caused by bad operator input rather than a server
// fault. The server layer maps it to HTTP 400 (vs 500 for everything else).
//
// It carries a dictionary CODE alongside the message, and the panel renders that
// code against its own dictionaries — the panel's language is a per-browser choice
// the server cannot see, so a message worded here would be stuck in one language on
// a bilingual page.
//
// Msg is not dead weight: it is the fallback the panel shows for a code it does not
// know, so a build whose dictionaries lag the server still reads as a sentence
// rather than as "err.tokenRequired". It is also what Error() returns, which is what
// lands in the logs and in the external REST API, where there is no dictionary.
type ValidationError struct {
	Msg  string
	Code string
	Args map[string]any
}

func (e *ValidationError) Error() string { return e.Msg }

// invalid builds a ValidationError with a formatted operator-facing message and no
// code. Kept for the messages the panel never surfaces; anything an operator can see
// should use invalidCode so it can be translated.
func invalid(format string, a ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, a...)}
}

// invalidCode builds a ValidationError the panel can translate. code names an entry
// under err.* in the frontend dictionaries; fallback is the wording shown when that
// entry is missing; args are interpolated into both.
//
// code comes first on purpose: a call site should read as the thing that went wrong,
// not as a sentence that happens to carry an id.
func invalidCode(code, fallback string, args ...map[string]any) error {
	e := &ValidationError{Msg: fallback, Code: code}
	if len(args) > 0 && args[0] != nil {
		e.Args = args[0]
		// Fill the fallback too: it is what a stale panel renders, and a bare
		// template with unfilled slots would read worse than the raw code.
		e.Msg = interpolate(fallback, args[0])
	}
	return e
}

// interpolate fills {{name}} placeholders. The panel does the same to the translated
// string; this keeps the untranslated path from showing braces to an operator.
func interpolate(s string, args map[string]any) string {
	for k, v := range args {
		s = strings.ReplaceAll(s, "{{"+k+"}}", fmt.Sprint(v))
	}
	return s
}

// fromFieldErr turns a model-layer validation failure into a ValidationError,
// carrying its code across the layer boundary. The model cannot build a
// ValidationError itself — that would invert the import — so it raises a FieldError
// and this is where the two meet. An uncoded error still becomes a 400; it just
// falls back to its own text in the panel.
func fromFieldErr(err error) error {
	var fe *model.FieldError
	if errors.As(err, &fe) {
		return &ValidationError{Msg: fe.Msg, Code: fe.Code, Args: fe.Args}
	}
	return invalid("%s", err.Error())
}
