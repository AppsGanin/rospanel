package nodeagent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// The panel compresses a config push for an agent that asks for gzip. The agent never
// asks by hand — Go's client does, and unpacks the answer itself — so this holds the
// real sync request to it: an agent that set the header itself, or turned compression
// off, would get the bytes still packed and fail every push.
func TestSyncReadsACompressedConfigPush(t *testing.T) {
	want := nodeapi.SyncResponse{Changed: true, AckReport: 3, State: &nodeapi.NodeState{
		Hash: "abc", XrayConfig: json.RawMessage(`{"inbounds":[{"tag":"vless-in"}]}`),
	}}
	var accepted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepted = r.Header.Get("Accept-Encoding")
		if !strings.Contains(accepted, "gzip") {
			_ = json.NewEncoder(w).Encode(want)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_ = json.NewEncoder(zw).Encode(want)
		_ = zw.Close()
	}))
	defer srv.Close()

	dir := t.TempDir()
	a := &Agent{
		dataDir:      dir,
		ident:        &Identity{PanelURL: srv.URL, NodeAPI: "seg", Token: "tok"},
		client:       &http.Client{Transport: syncTransport(false)},
		sup:          xray.NewSupervisor("", filepath.Join(dir, "config.json"), dir),
		certPath:     filepath.Join(dir, "cert.pem"),
		state:        &persistState{},
		pending:      map[int64]*nodeapi.TrafficDelta{},
		inflight:     map[int64]*nodeapi.TrafficDelta{},
		lastCounters: map[string]xray.Traffic{},
	}
	got, err := a.syncOnce(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !strings.Contains(accepted, "gzip") {
		t.Fatalf("the sync request did not accept gzip (Accept-Encoding %q)", accepted)
	}
	if !got.Changed || got.AckReport != 3 || got.State == nil || got.State.Hash != "abc" ||
		string(got.State.XrayConfig) != string(want.State.XrayConfig) {
		t.Fatalf("decoded %+v", got)
	}
}
