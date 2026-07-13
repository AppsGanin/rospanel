package nodeagent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

func TestSplitJoinURL(t *testing.T) {
	cases := []struct {
		in      string
		wantTok string
		wantErr bool
	}{
		{"https://panel.example.com/abc/v1/join#rpn_tok", "rpn_tok", false},
		{"http://127.0.0.1:8080/abc/v1/join#rpn_tok", "rpn_tok", false}, // loopback http ok
		{"http://panel.example.com/abc/v1/join#rpn_tok", "", true},      // http to non-loopback rejected
		{"https://panel.example.com/abc/v1/join", "", true},             // no token
		{"/abc/v1/join#tok", "", true},                                  // not absolute
	}
	for _, c := range cases {
		base, tok, err := splitJoinURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected error, got base=%q tok=%q", c.in, base, tok)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if tok != c.wantTok {
			t.Errorf("%q: token = %q, want %q", c.in, tok, c.wantTok)
		}
		if strings.Contains(base, "#") {
			t.Errorf("%q: base still has fragment: %q", c.in, base)
		}
	}
}

func TestJoinPersistsIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nodeapi.JoinRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.JoinToken != "rpn_join" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(nodeapi.JoinResponse{
			NodeID: 42, Token: "rpn_perm", PanelURL: "https://panel.example.com", NodeAPI: "seg",
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	// The stub server is http on loopback → allowed. Build the join URL against it.
	joinURL := srv.URL + "/seg/v1/join#rpn_join"
	id, err := Join(dir, joinURL, false)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if id.NodeID != 42 || id.Token != "rpn_perm" {
		t.Fatalf("identity = %+v", id)
	}
	// Persisted and reloadable.
	got, err := LoadIdentity(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Token != "rpn_perm" || got.PanelURL != "https://panel.example.com" || got.NodeAPI != "seg" {
		t.Fatalf("reloaded identity = %+v", got)
	}
	// node.json is 0600.
	fi, _ := os.Stat(filepath.Join(dir, "node.json"))
	if fi != nil && fi.Mode().Perm() != 0o600 {
		t.Errorf("node.json perms = %v, want 0600", fi.Mode().Perm())
	}
}

func TestAckReportClearsBuffer(t *testing.T) {
	a := &Agent{
		inflight:   map[int64]*nodeapi.TrafficDelta{1: {UserID: 1, Up: 100}},
		inflightID: 5,
	}
	// A stale ack (below the in-flight batch's report id) doesn't clear it.
	a.ackReport(4)
	if len(a.inflight) != 1 {
		t.Fatal("stale ack should not clear the in-flight batch")
	}
	// An ack at-or-above the batch's report id clears it.
	a.ackReport(5)
	if len(a.inflight) != 0 || a.inflightID != 0 {
		t.Fatalf("ack did not clear batch: inflight=%d rid=%d", len(a.inflight), a.inflightID)
	}
}

func TestBuildSyncRequestAssignsReportID(t *testing.T) {
	dir := t.TempDir()
	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	a := &Agent{
		sup:          sup,
		certPath:     filepath.Join(dir, "cert.pem"),
		state:        &persistState{},
		pending:      map[int64]*nodeapi.TrafficDelta{7: {UserID: 7, Up: 10, Down: 20}},
		inflight:     map[int64]*nodeapi.TrafficDelta{},
		lastCounters: map[string]xray.Traffic{},
	}
	req := a.buildSyncRequest()
	if req.ReportID == 0 {
		t.Fatal("expected a non-zero report id when traffic is pending")
	}
	if len(req.Traffic) != 1 || req.Traffic[0].UserID != 7 {
		t.Fatalf("traffic not included: %+v", req.Traffic)
	}
	if !req.CertSelfSigned {
		t.Fatal("no cert on disk should report self-signed")
	}
	// A second call with no new traffic (batch still in flight) resends the SAME
	// report id — resending a new id would double-count on the panel.
	first := req.ReportID
	req2 := a.buildSyncRequest()
	if req2.ReportID != first || len(req2.Traffic) != 1 {
		t.Fatalf("resend changed the batch: id %d → %d, traffic %d", first, req2.ReportID, len(req2.Traffic))
	}
	// After the ack, the in-flight batch clears; a subsequent send has no traffic.
	a.ackReport(first)
	req3 := a.buildSyncRequest()
	if len(req3.Traffic) != 0 {
		t.Fatalf("acked batch should be gone, got %+v", req3.Traffic)
	}
}

func TestUserIDFromEmail(t *testing.T) {
	for _, c := range []struct {
		email string
		id    int64
		ok    bool
	}{
		{"u42", 42, true},
		{"u0", 0, true},
		{"x1", 0, false},
		{"u", 0, false},
		{"uabc", 0, false},
	} {
		id, ok := userIDFromEmail(c.email)
		if ok != c.ok || id != c.id {
			t.Errorf("%q → (%d,%v), want (%d,%v)", c.email, id, ok, c.id, c.ok)
		}
	}
}
