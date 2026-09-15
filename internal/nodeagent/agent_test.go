package nodeagent

import (
	"encoding/json"
	"fmt"
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
		dataDir:      dir,
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

// TestReportIDPersistsAcrossRestart locks in the fix for the silent-traffic-loss bug:
// a promoted report id is persisted to state.json and resumed on the next process, so
// the panel's forward-only watermark keeps accepting the node's traffic after a restart.
func TestReportIDPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	mk := func(st *persistState, pending map[int64]*nodeapi.TrafficDelta) *Agent {
		return &Agent{
			dataDir:      dir,
			sup:          xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir),
			certPath:     filepath.Join(dir, "cert.pem"),
			state:        st,
			pending:      pending,
			inflight:     map[int64]*nodeapi.TrafficDelta{},
			lastCounters: map[string]xray.Traffic{},
		}
	}
	// First run: send some batches so the report id climbs, then ack them.
	a := mk(&persistState{}, map[int64]*nodeapi.TrafficDelta{7: {UserID: 7, Up: 10}})
	r1 := a.buildSyncRequest()
	a.ackReport(r1.ReportID)
	a.pending[8] = &nodeapi.TrafficDelta{UserID: 8, Up: 20}
	r2 := a.buildSyncRequest()
	a.ackReport(r2.ReportID)
	if r2.ReportID <= r1.ReportID {
		t.Fatalf("report id should climb: %d then %d", r1.ReportID, r2.ReportID)
	}

	// Simulate a restart: reload state from disk and seed reportSeq exactly as newAgent
	// does, then send fresh traffic. Its id must exceed the pre-restart watermark.
	reloaded := loadState(dir)
	if reloaded.LastReportID != r2.ReportID {
		t.Fatalf("persisted watermark = %d, want %d", reloaded.LastReportID, r2.ReportID)
	}
	b := mk(reloaded, map[int64]*nodeapi.TrafficDelta{9: {UserID: 9, Up: 30}})
	b.reportSeq = b.state.LastReportID
	r3 := b.buildSyncRequest()
	if r3.ReportID <= r2.ReportID {
		t.Fatalf("post-restart report id %d must exceed pre-restart watermark %d — "+
			"the panel would drop it as a duplicate", r3.ReportID, r2.ReportID)
	}
}

func TestSubstituteCertPaths(t *testing.T) {
	raw := []byte(`{"certificateFile":"` + nodeapi.CertPathSentinel +
		`","keyFile":"` + nodeapi.KeyPathSentinel + `"}`)
	out := substituteCertPaths(raw, "/var/lib/rospanel-node/certs/cert.pem", "/var/lib/rospanel-node/certs/key.pem")
	s := string(out)
	if strings.Contains(s, nodeapi.CertPathSentinel) || strings.Contains(s, nodeapi.KeyPathSentinel) {
		t.Fatalf("sentinels not fully substituted: %s", s)
	}
	if !strings.Contains(s, "/var/lib/rospanel-node/certs/cert.pem") ||
		!strings.Contains(s, "/var/lib/rospanel-node/certs/key.pem") {
		t.Fatalf("absolute paths missing: %s", s)
	}
}

func TestValidPanelURL(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"https://panel.example.com", true},
		{"https://panel.example.com/", true},
		{"http://panel.example.com", false}, // must be https
		{"https://", false},                 // no host
		{"not a url", false},
		{"", false},
	} {
		if got := validPanelURL(c.in); got != c.ok {
			t.Errorf("validPanelURL(%q) = %v, want %v", c.in, got, c.ok)
		}
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

// A traffic backlog goes out in chunks the panel's body cap can take: every user's
// traffic exactly once, each chunk under its own report id and resent unchanged until
// acked, flagged as more-to-come while anything is left behind and not on the last.
func TestTrafficBacklogGoesOutInChunks(t *testing.T) {
	dir := t.TempDir()
	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	total := 2*trafficChunkMax + 500
	pending := make(map[int64]*nodeapi.TrafficDelta, total)
	for i := 1; i <= total; i++ {
		pending[int64(i)] = &nodeapi.TrafficDelta{UserID: int64(i), Up: int64(i), Down: int64(2 * i)}
	}
	a := &Agent{
		dataDir: dir, sup: sup, certPath: filepath.Join(dir, "cert.pem"),
		state: &persistState{}, pending: pending,
		inflight: map[int64]*nodeapi.TrafficDelta{}, lastCounters: map[string]xray.Traffic{},
	}

	seen := map[int64]bool{}
	var lastID int64
	for round := 0; ; round++ {
		req := a.buildSyncRequest()
		if len(req.Traffic) == 0 {
			if req.TrafficMore {
				t.Fatal("a request with no traffic said more was coming")
			}
			break
		}
		if round > 3 {
			t.Fatalf("still sending after %d chunks", round)
		}
		if len(req.Traffic) > trafficChunkMax {
			t.Fatalf("chunk of %d, cap %d", len(req.Traffic), trafficChunkMax)
		}
		if req.ReportID <= lastID {
			t.Fatalf("chunk report id %d not above the previous %d", req.ReportID, lastID)
		}
		lastID = req.ReportID
		if resend := a.buildSyncRequest(); resend.ReportID != req.ReportID || len(resend.Traffic) != len(req.Traffic) || resend.TrafficMore != req.TrafficMore {
			t.Fatalf("unacked chunk not resent unchanged: id %d→%d, %d→%d rows", req.ReportID, resend.ReportID, len(req.Traffic), len(resend.Traffic))
		}
		for _, d := range req.Traffic {
			if seen[d.UserID] {
				t.Fatalf("user %d sent twice", d.UserID)
			}
			if d.Up != d.UserID || d.Down != 2*d.UserID {
				t.Fatalf("user %d sent %d/%d", d.UserID, d.Up, d.Down)
			}
			seen[d.UserID] = true
		}
		if wantMore := len(seen) < total; req.TrafficMore != wantMore {
			t.Fatalf("after %d of %d users, more = %v", len(seen), total, req.TrafficMore)
		}
		a.ackReport(req.ReportID)
	}
	if len(seen) != total {
		t.Fatalf("%d of %d users' traffic went out", len(seen), total)
	}

	// A partial batch in flight while a new sample lands is not a backlog: the sample
	// goes out on the next poll, which the panel may hold.
	a.pending = map[int64]*nodeapi.TrafficDelta{1: {UserID: 1, Up: 5}}
	first := a.buildSyncRequest()
	a.pending[2] = &nodeapi.TrafficDelta{UserID: 2, Up: 6}
	if again := a.buildSyncRequest(); again.ReportID != first.ReportID || again.TrafficMore {
		t.Fatalf("a partial in-flight batch with a new sample: id %d→%d more=%v", first.ReportID, again.ReportID, again.TrafficMore)
	}
}

// More distinct user+address pairs than one sync carries go out over consecutive
// syncs, each marked while more are waiting — none dropped, none twice. The buffer
// still has a bound against a flood.
func TestConnSamplesGoOutInChunks(t *testing.T) {
	dir := t.TempDir()
	sup := xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir)
	a := &Agent{
		dataDir: dir, sup: sup, certPath: filepath.Join(dir, "cert.pem"),
		state: &persistState{}, pending: map[int64]*nodeapi.TrafficDelta{},
		inflight: map[int64]*nodeapi.TrafficDelta{}, lastCounters: map[string]xray.Traffic{},
		conns: map[string]nodeapi.ConnSample{}, sites: map[siteKey]int64{},
	}
	total := 2*connsChunkMax + 100
	for i := 0; i < total; i++ {
		a.recordConn(fmt.Sprintf("u%d", 1+i%5000), fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255), "")
	}
	seen := map[string]bool{}
	for round := 0; ; round++ {
		req := a.buildSyncRequest()
		if len(req.Conns) == 0 {
			if req.ConnsMore {
				t.Fatal("a sync with no samples said more were waiting")
			}
			break
		}
		if round > 3 || len(req.Conns) > connsChunkMax {
			t.Fatalf("round %d carried %d samples, cap %d", round, len(req.Conns), connsChunkMax)
		}
		for _, c := range req.Conns {
			k := c.Email + " " + c.IP
			if seen[k] {
				t.Fatalf("%s sent twice", k)
			}
			seen[k] = true
		}
		if wantMore := len(seen) < total; req.ConnsMore != wantMore {
			t.Fatalf("after %d of %d samples, more = %v", len(seen), total, req.ConnsMore)
		}
	}
	if len(seen) != total {
		t.Fatalf("%d of %d samples went out", len(seen), total)
	}

	for i := 0; i < connsMax+500; i++ {
		a.recordConn("u1", fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255), "")
	}
	if n := len(a.conns); n != connsMax {
		t.Fatalf("the buffer holds %d samples, bound %d", n, connsMax)
	}
}
