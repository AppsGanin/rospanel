package model

import (
	"fmt"
	"strings"
)

// FieldError is a validation failure that the panel can translate.
//
// The model layer cannot reach core.ValidationError (that would invert the import),
// so it raises this instead and core re-wraps it, carrying the code through. Without
// it these messages would arrive at the panel as prose the server wrote, stuck in
// one language on a page the admin can switch.
//
// Msg is the fallback shown for a code the panel has no entry for, so a stale build
// still reads as a sentence rather than as "err.inboundPortRange".
type FieldError struct {
	Code string
	Msg  string
	Args map[string]any
}

func (e *FieldError) Error() string { return e.Msg }

// FieldErr is the exported constructor, for leaf packages that validate operator
// input but sit below core — branding is the one that does. Same contract as the
// package-internal fieldErr.
func FieldErr(code, fallback string, args ...map[string]any) *FieldError {
	return fieldErr(code, fallback, args...)
}

// fieldErr builds a FieldError, filling {{name}} slots in the fallback so the
// untranslated path never shows braces to an operator.
func fieldErr(code, fallback string, args ...map[string]any) *FieldError {
	e := &FieldError{Code: code, Msg: fallback}
	if len(args) > 0 && args[0] != nil {
		e.Args = args[0]
		for k, v := range args[0] {
			e.Msg = strings.ReplaceAll(e.Msg, "{{"+k+"}}", fmt.Sprint(v))
		}
	}
	return e
}
