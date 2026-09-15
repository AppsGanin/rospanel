package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// A config push goes out compressed to a node that accepts gzip, and as it is to one
// that does not; an answer without a config is never compressed.
func TestConfigPushIsCompressedWhenTheNodeAcceptsIt(t *testing.T) {
	rt, _ := rolesTestRouter(t)
	_, token := joinedNode(t, rt, "berlin")

	post := func(accept string, req nodeapi.SyncRequest) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/v1/sync", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		if accept != "" {
			r.Header.Set("Accept-Encoding", accept)
		}
		rec := httptest.NewRecorder()
		rt.handleNodeSync(rec, r)
		return rec
	}
	read := func(rec *httptest.ResponseRecorder) (nodeapi.SyncResponse, bool) {
		t.Helper()
		body := io.Reader(rec.Body)
		zipped := rec.Header().Get("Content-Encoding") == "gzip"
		if zipped {
			zr, err := gzip.NewReader(rec.Body)
			if err != nil {
				t.Fatalf("a gzip-labelled body is not gzip: %v", err)
			}
			body = zr
		}
		var resp nodeapi.SyncResponse
		if err := json.NewDecoder(body).Decode(&resp); err != nil {
			t.Fatalf("sync body: %v", err)
		}
		return resp, zipped
	}

	resp, zipped := read(post("gzip", nodeapi.SyncRequest{}))
	if !zipped || !resp.Changed || resp.State == nil || len(resp.State.XrayConfig) == 0 {
		t.Fatalf("push to a node accepting gzip: zipped=%v changed=%v", zipped, resp.Changed)
	}
	if rec := post("gzip", nodeapi.SyncRequest{}); rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("a compressed answer does not say it varies by Accept-Encoding")
	}
	hash := resp.State.Hash

	for _, accept := range []string{"", "identity", "gzip;q=0", "deflate, br"} {
		resp, zipped := read(post(accept, nodeapi.SyncRequest{}))
		if zipped || resp.State == nil || resp.State.Hash != hash {
			t.Errorf("Accept-Encoding %q: zipped=%v, state %v", accept, zipped, resp.State != nil)
		}
	}

	// An answer with no config: a counted chunk of a backlog is answered at once.
	chunk := nodeapi.SyncRequest{ConfigHash: hash, ReportID: 1, TrafficMore: true,
		Traffic: []nodeapi.TrafficDelta{{UserID: 424242, Up: 1, Down: 1}}}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- post("gzip", chunk) }()
	select {
	case rec := <-done:
		if resp, zipped := read(rec); zipped || resp.State != nil || resp.AckReport != 1 {
			t.Fatalf("an answer without a config: zipped=%v state=%v ack=%d", zipped, resp.State != nil, resp.AckReport)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the chunk was held")
	}
}

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"":                      false,
		"gzip":                  true,
		"GZIP":                  true,
		"deflate, gzip":         true,
		"gzip;q=0.5, br":        true,
		"br, gzip ; q=1":        true,
		"gzip;q=0":              false,
		"gzip; q=0.0":           false,
		"identity":              false,
		"x-gzip":                false,
		"deflate, br;q=0, zstd": false,
	} {
		if got := acceptsGzip(header); got != want {
			t.Errorf("%q: %v, want %v", header, got, want)
		}
	}
}
