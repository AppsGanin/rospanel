package xray

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeAPIXray is an `xray` whose api calls fail to dial while the file dial-fails holds
// a count above zero — or dial-fails-<call>, for that call only — taking one off each
// time; fail after connecting when the file other-fails exists; and otherwise succeed.
// Every api call is logged, one per line.
func fakeAPIXray(t *testing.T) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "xray")
	calls := filepath.Join(dir, "calls.log")
	dial := filepath.Join(dir, "dial-fails")
	other := filepath.Join(dir, "other-fails")
	script := "#!/bin/sh\n" +
		"[ \"$1\" = api ] || exit 0\n" +
		"echo \"$2\" >> " + calls + "\n" +
		"for f in " + dial + " " + dial + "-$2; do if [ -f $f ]; then n=$(cat $f); if [ \"$n\" -gt 0 ]; then echo $((n-1)) > $f; echo 'failed to dial 127.0.0.1:10085' >&2; exit 1; fi; fi; done\n" +
		"if [ -f " + other + " ]; then echo 'failed to add users: rejected' >&2; exit 1; fi\n" +
		"if [ \"$2\" = adi ] && [ ! -s \"$4\" ]; then echo 'cannot read the inbound file' >&2; exit 1; fi\n" +
		"if [ \"$2\" = adu ]; then n=$(grep -o '\"email\":\"[^\"]*\"' \"$4\" | wc -l | tr -d ' '); echo \"Added $n user(s) in total.\"; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func apiCalls(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func retrySupervisor(t *testing.T) (*Supervisor, string, *[]time.Duration) {
	t.Helper()
	bin, dir := fakeAPIXray(t)
	s := NewSupervisor(bin, filepath.Join(dir, "config.json"), dir)
	var waits []time.Duration
	s.waitFn = func(d time.Duration) { waits = append(waits, d) }
	return s, dir, &waits
}

func setDialFailures(t *testing.T, dir string, n int) { setCallDialFailures(t, dir, "", n) }

// setCallDialFailures makes the next n dials of one call fail; call "" is any call.
func setCallDialFailures(t *testing.T, dir, call string, n int) {
	t.Helper()
	name := "dial-fails"
	if call != "" {
		name += "-" + call
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strconv.Itoa(n)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A live change whose call cannot reach Xray is asked again, with growing waits, and
// succeeds once Xray answers — instead of handing the failure to a full reload.
func TestAnAPICallThatCannotReachXrayIsAskedAgain(t *testing.T) {
	s, dir, waits := retrySupervisor(t)
	setDialFailures(t, dir, 2)
	if err := s.RemoveUsers("127.0.0.1:10085", []string{"vless-in"}, []string{"u1"}); err != nil {
		t.Fatalf("removal after two failed dials: %v", err)
	}
	if got := apiCalls(t, dir); !slices.Equal(got, []string{"rmu", "rmu", "rmu"}) {
		t.Errorf("calls %v, want the removal asked three times", got)
	}
	if want := apiDialRetries[:2]; !slices.Equal(*waits, want) {
		t.Errorf("waited %v, want %v", *waits, want)
	}
}

// Asked again only so often: a call that never reaches Xray fails once the tries are
// spent, and the caller falls back as before.
func TestAnAPICallGivesUpOnceItsTriesAreSpent(t *testing.T) {
	s, dir, waits := retrySupervisor(t)
	setDialFailures(t, dir, 9)
	err := s.RemoveUsers("127.0.0.1:10085", []string{"vless-in"}, []string{"u1"})
	if err == nil || !strings.Contains(err.Error(), "failed to dial") {
		t.Fatalf("a call that never reached xray: %v", err)
	}
	if got := len(apiCalls(t, dir)); got != 1+len(apiDialRetries) {
		t.Errorf("asked %d times, want %d", got, 1+len(apiDialRetries))
	}
	if !slices.Equal(*waits, apiDialRetries) {
		t.Errorf("waited %v, want %v", *waits, apiDialRetries)
	}
}

// A call that reached Xray and failed there may have changed something, so it is not
// asked again: its failure goes straight back.
func TestAnAPICallThatReachedXrayIsNotAskedAgain(t *testing.T) {
	s, dir, waits := retrySupervisor(t)
	if err := os.WriteFile(filepath.Join(dir, "other-fails"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	in := Inbound{Tag: "vless-in", Protocol: "vless", Settings: VLESSInboundSettings{Clients: []VLESSClient{{ID: "uuid-1", Email: "u1"}}}}
	if err := s.AddUsers("127.0.0.1:10085", []Inbound{in}); err == nil {
		t.Fatal("a failure after connecting was not returned")
	}
	if got := apiCalls(t, dir); !slices.Equal(got, []string{"adu"}) || len(*waits) != 0 {
		t.Errorf("calls %v, waits %v — want one call and no wait", got, *waits)
	}
}

// The calls that hand Xray a file keep the file for every try: users added after a
// failed dial, and an inbound rebuilt after one.
func TestCallsWithAFileAreAskedAgainWithTheFile(t *testing.T) {
	s, dir, _ := retrySupervisor(t)
	in := Inbound{Tag: "vless-in", Protocol: "vless", Settings: VLESSInboundSettings{Clients: []VLESSClient{{ID: "uuid-1", Email: "u1"}, {ID: "uuid-2", Email: "u2"}}}}
	setDialFailures(t, dir, 1)
	if err := s.AddUsers("127.0.0.1:10085", []Inbound{in}); err != nil {
		t.Fatalf("adding after a failed dial: %v", err)
	}
	setDialFailures(t, dir, 2) // rmi takes both, adi is then asked once
	if err := s.replaceInbound("127.0.0.1:10085", in.Tag, in); err != nil {
		t.Fatalf("rebuilding after failed dials: %v", err)
	}
	setCallDialFailures(t, dir, "adi", 1) // the removal goes through, the add fails to dial
	if err := s.replaceInbound("127.0.0.1:10085", in.Tag, in); err != nil {
		t.Fatalf("rebuilding after a failed dial on the add: %v", err)
	}
	want := []string{"adu", "adu", "rmi", "rmi", "rmi", "adi", "rmi", "adi", "adi"}
	if got := apiCalls(t, dir); !slices.Equal(got, want) {
		t.Errorf("calls %v, want %v", got, want)
	}
}
