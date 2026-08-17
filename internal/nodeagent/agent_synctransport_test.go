package nodeagent

import "testing"

// The long-poll must not use HTTP/2: over the panel's :443 Xray-fallback an h2 hold
// gets GOAWAY'd mid-poll. Guard that the transport keeps h2 disabled.
func TestSyncTransportForcesHTTP1(t *testing.T) {
	tr := syncTransport(false)
	if tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 is true; the sync long-poll would negotiate h2 and get GOAWAY'd")
	}
	if tr.TLSNextProto == nil {
		t.Error("TLSNextProto is nil; h2 auto-upgrade is not disabled")
	}
	if len(tr.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto has %d entries, want an empty map (no h2)", len(tr.TLSNextProto))
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("secure transport must not skip TLS verification")
	}
	// Insecure opt-in still forces h1 and only then skips verification.
	ins := syncTransport(true)
	if ins.TLSNextProto == nil || len(ins.TLSNextProto) != 0 {
		t.Error("insecure transport did not disable h2")
	}
	if ins.TLSClientConfig == nil || !ins.TLSClientConfig.InsecureSkipVerify {
		t.Error("insecure transport should skip verification")
	}
}
