package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The rescue command prints a one-time password to the operator's terminal, which is the
// justification for it not being "clear-text logging". That justification stops holding
// the moment stdout is redirected: the password then lands in a file or a pipe and
// outlives the moment it was needed. Warn, so the operator knows there is a copy.
func TestRescueWarnsWhenStdoutIsRedirected(t *testing.T) {
	if os.Getenv("RESCUE_PRINT_CHILD") == "1" {
		printRescueCredentials("admin", "s3cret-example", false)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRescueWarnsWhenStdoutIsRedirected")
	cmd.Env = append(os.Environ(), "RESCUE_PRINT_CHILD=1")
	out, err := cmd.Output() // a pipe, i.e. not a terminal
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	if !strings.Contains(string(out), "WARNING") {
		t.Errorf("no warning when stdout is a pipe — the operator is not told the "+
			"password was captured somewhere:\n%s", out)
	}
	if !strings.Contains(string(out), "s3cret-example") {
		t.Error("the credentials themselves stopped being printed — the command is now useless")
	}
}
