package xray

import "testing"

// The raw `xray -test` output opens with a version banner and a temp path; what an
// operator needs is the root cause at the end of the chain.
func TestCleanValidationError(t *testing.T) {
	raw := `Xray 26.7.28 (Xray, Penetrates Everything.) 45cf289 (go1.26.4 linux/amd64)
A unified platform for anti-censorship.
Failed to start: main: failed to load config files: [/tmp/xray-check-267.json] > infra/conf: failed to build inbound config with tag custom-0 > infra/conf: Failed to build sockopt. > infra/conf: unsupported domain strategy: nonsense`
	got := cleanValidationError(raw)
	want := "infra/conf: unsupported domain strategy: nonsense"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if out := cleanValidationError("Xray 26.7.28 (Xray)\nA unified platform for anti-censorship."); out == "" {
		t.Error("output with nothing but a banner should still say something")
	}
}
