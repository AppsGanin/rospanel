package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
)

// stuckWriter is a node on a link that has stopped taking bytes: its first Write
// announces itself and waits until let go.
type stuckWriter struct {
	header  http.Header
	status  int
	entered chan struct{}
	letGo   chan struct{}
	once    sync.Once
	body    bytes.Buffer
}

func newStuckWriter() *stuckWriter {
	return &stuckWriter{header: http.Header{}, entered: make(chan struct{}), letGo: make(chan struct{})}
}

func (s *stuckWriter) Header() http.Header  { return s.header }
func (s *stuckWriter) WriteHeader(code int) { s.status = code }
func (s *stuckWriter) Write(p []byte) (int, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.letGo
	return s.body.Write(p)
}

func pushNode(t *testing.T, st *store.Store, name string) *model.Node {
	t.Helper()
	n, err := st.CreateNode(name, name+".example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if err := st.UpdateNode(n.ID, store.NodeEdit{Name: n.Name, Host: n.Host, VLESS: &yes}); err != nil {
		t.Fatal(err)
	}
	n, err = st.GetNode(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A node on a link that has stopped taking bytes holds up nobody else's config: the
// push is encoded — and the state let go of — before its first byte is written, so
// the next node's state is built while the first write is still stuck. What is
// finally written is the whole response, compressed or not.
func TestAStuckNodeHoldsUpNoOtherPush(t *testing.T) {
	for _, gz := range []bool{true, false} {
		t.Run(map[bool]string{true: "gzip", false: "plain"}[gz], func(t *testing.T) {
			rt, st := rolesTestRouter(t)
			if _, err := st.CreateUser("a", "uuid-a", "pw", "tok-a", 0, 0, 0); err != nil {
				t.Fatal(err)
			}
			slow, other := pushNode(t, st, "slow"), pushNode(t, st, "other")

			state, done, err := rt.mgr.NodeStatePush(context.Background(), slow, "")
			if err != nil || state == nil {
				t.Fatalf("no state for the slow node: %v", err)
			}
			hash := state.Hash
			resp := &nodeapi.SyncResponse{Changed: true, State: state}
			req := httptest.NewRequest("POST", "/", nil)
			if gz {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			var encoded atomic.Bool
			w := newStuckWriter()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				writeSyncResponse(w, req, resp, func() { encoded.Store(true); done() })
			}()
			select {
			case <-w.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("the response was never written")
			}
			if !encoded.Load() {
				t.Error("writing began before the push was marked encoded: the gate is held for the write")
			}
			if resp.State != nil {
				t.Error("the response still holds the state while it is written out")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			next, doneNext, err := rt.mgr.NodeStatePush(ctx, other, "")
			if err != nil || next == nil {
				t.Fatalf("another node's push waited on a stuck write: %v", err)
			}
			doneNext()

			close(w.letGo)
			<-finished
			var body io.Reader = &w.body
			if gz {
				if w.header.Get("Content-Encoding") != "gzip" {
					t.Fatalf("asked for gzip, got %q", w.header.Get("Content-Encoding"))
				}
				zr, err := gzip.NewReader(&w.body)
				if err != nil {
					t.Fatal(err)
				}
				body = zr
			} else if w.header.Get("Content-Encoding") != "" {
				t.Fatalf("not asked for gzip, got %q", w.header.Get("Content-Encoding"))
			}
			var got nodeapi.SyncResponse
			if err := json.NewDecoder(body).Decode(&got); err != nil {
				t.Fatalf("the written response does not decode: %v", err)
			}
			if w.status != http.StatusOK || !got.Changed || got.State == nil || got.State.Hash != hash || len(got.State.XrayConfig) == 0 {
				t.Errorf("written: status %d, changed %v, state %v", w.status, got.Changed, got.State != nil)
			}
		})
	}
}

// Every way a sync request can end lets the state gate go: a push answered at once, a
// push after a held poll is woken, a node told it is revoked, and a node that hangs up
// while held. Were any to keep it, the next push anywhere would wait for good.
func TestEverySyncPathLetsTheStateGateGo(t *testing.T) {
	rt, st := rolesTestRouter(t)
	id, token := joinedNode(t, rt, "berlin")
	gateFree := func(when string) {
		t.Helper()
		n, err := st.GetNode(id)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// A hash no node has, so the push must build and take the gate.
		state, done, err := rt.mgr.NodeStatePush(ctx, n, "not-a-hash")
		if err != nil || state == nil {
			t.Fatalf("%s: the gate was not free: %v", when, err)
		}
		done()
	}
	decode := func(rec *httptest.ResponseRecorder) nodeapi.SyncResponse {
		t.Helper()
		var resp nodeapi.SyncResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("sync body: %v", err)
		}
		return resp
	}

	first := decode(syncRequest(rt, token, nodeapi.SyncRequest{}))
	if !first.Changed || first.State == nil {
		t.Fatal("a new node was not pushed a state")
	}
	gateFree("after a push answered at once")

	// Held with the state it has, then woken by a change: pushed after the hold.
	held := make(chan nodeapi.SyncResponse, 1)
	go func() { held <- decode(syncRequest(rt, token, nodeapi.SyncRequest{ConfigHash: first.State.Hash})) }()
	time.Sleep(300 * time.Millisecond) // parked
	// The node's own DNS servers: in its config, and the edit wakes it.
	dns := "9.9.9.9"
	if err := rt.mgr.SetNodeDNS(id, &dns); err != nil {
		t.Fatal(err)
	}
	select {
	case resp := <-held:
		if !resp.Changed || resp.State == nil {
			t.Fatalf("the woken poll was not pushed the change: %+v", resp.Changed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the held poll was never answered")
	}
	gateFree("after a push that followed a hold")

	// Hung up while held.
	raw, _ := json.Marshal(nodeapi.SyncRequest{ConfigHash: "whatever-it-has"})
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/v1/sync", bytes.NewReader(raw)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+token)
	gone := make(chan struct{})
	go func() { rt.handleNodeSync(httptest.NewRecorder(), r); close(gone) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-gone
	gateFree("after a node hung up")

	// Switched off: told it is revoked, given no state.
	if err := st.SetNodeEnabled(id, false); err != nil {
		t.Fatal(err)
	}
	if resp := decode(syncRequest(rt, token, nodeapi.SyncRequest{})); !resp.Revoked || resp.State != nil {
		t.Errorf("a switched-off node: revoked %v, state %v", resp.Revoked, resp.State != nil)
	}
	gateFree("after a node was told it is revoked")
}
