package core

import "fmt"

// ValidationError marks an error caused by bad operator input rather than a
// server fault. The server layer maps it to HTTP 400 (vs 500 for everything
// else); its message is operator-facing (Russian) and safe to show in the UI.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// invalid builds a ValidationError with a formatted operator-facing message.
func invalid(format string, a ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, a...)}
}
