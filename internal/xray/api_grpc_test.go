package xray

import (
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The answer to a gRPC call is in its trailers — or, for an error with no body, in its
// headers alone — and anything but status 0 is a failure, carrying Xray's message.
func TestXrayAPIReadsTheCallStatus(t *testing.T) {
	cases := map[string]struct {
		handler http.HandlerFunc
		wantErr string
	}{
		"ok": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Trailer", "Grpc-Status")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte{0, 0, 0, 0, 0})
			w.Header().Set("Grpc-Status", "0")
		}, ""},
		"refused in the trailers": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Grpc-Status", "2")
			w.Header().Set("Grpc-Message", "app/proxyman/command: handler%20not%20found")
		}, "grpc status 2: app/proxyman/command: handler not found"},
		"refused in the headers alone": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Grpc-Status", "12")
			w.Header().Set("Grpc-Message", "unknown method")
			w.WriteHeader(http.StatusOK)
		}, "grpc status 12: unknown method"},
		"no status at all": {func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}, "no grpc-status"},
		"not grpc": {func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusNotFound)
		}, "http status 404"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			addr := serveH2C(t, "127.0.0.1:0", c.handler)
			api := newXrayAPI(addr, func(time.Duration) { t.Error("a call that reached the server was asked again") })
			defer api.close()
			err := api.removeUser("hysteria-in", "u1")
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("err %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("err %v, want one containing %q", err, c.wantErr)
			}
		})
	}
}

// A call that cannot connect sent nothing, so it is asked again with the same waits as
// the CLI's calls — and gets through once Xray listens.
func TestXrayAPIAsksAgainWhileXrayCannotBeReached(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	fake := &fakeHandlerAPI{}
	var waits []time.Duration
	api := newXrayAPI(addr, func(d time.Duration) {
		waits = append(waits, d)
		if len(waits) == 2 {
			serveH2C(t, addr, fake.ServeHTTP)
		}
	})
	defer api.close()
	if err := api.addHysteriaUser("hysteria-in", "u1", "pw-u1"); err != nil {
		t.Fatalf("after xray came up: %v", err)
	}
	if !slices.Equal(waits, apiDialRetries[:2]) {
		t.Errorf("waited %v, want %v", waits, apiDialRetries[:2])
	}
	if got := fake.calls(); !slices.Equal(got, []string{"add u1:pw-u1 to hysteria-in"}) {
		t.Errorf("calls %q", got)
	}

	// Never up: the tries run out and the failure is returned.
	waits = nil
	l2, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := l2.Addr().String()
	l2.Close()
	api2 := newXrayAPI(dead, func(d time.Duration) { waits = append(waits, d) })
	defer api2.close()
	if err := api2.removeUser("hysteria-in", "u1"); err == nil {
		t.Fatal("a call to nothing succeeded")
	}
	if !slices.Equal(waits, apiDialRetries) {
		t.Errorf("waited %v, want %v", waits, apiDialRetries)
	}
}

// An empty email or auth never reaches Xray: an empty auth would be a key anyone holds.
func TestXrayAPIRefusesAnEmptyUser(t *testing.T) {
	api := newXrayAPI("127.0.0.1:1", func(time.Duration) { t.Error("dialled") })
	defer api.close()
	for _, u := range [][2]string{{"", "pw"}, {"u1", ""}} {
		if err := api.addHysteriaUser("hysteria-in", u[0], u[1]); err == nil {
			t.Errorf("added %q with auth %q", u[0], u[1])
		}
	}
}

// A message over the limit is refused before anything is dialled, and not asked again:
// its length would not fit the frame's, and Xray would refuse it anyway.
func TestXrayAPIRefusesAnOversizedMessage(t *testing.T) {
	api := newXrayAPI("127.0.0.1:1", func(time.Duration) { t.Error("asked again") })
	defer api.close()
	if _, err := api.call(handlerAlterInbound, make([]byte, apiMaxMessage+1)); err == nil {
		t.Fatal("an oversized message was sent")
	}
}

// serveH2C serves handler over cleartext HTTP/2 on addr and returns the address.
func serveH2C(t *testing.T, addr string, handler http.HandlerFunc) string {
	t.Helper()
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: handler, Protocols: &protocols}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// The quota watch reads the counters through the gRPC API, not the CLI: the answer's
// per-user counters come back folded as QueryStats folds the CLI's, and nothing else.
func TestQueryStatsLiveReadsTheCounters(t *testing.T) {
	stat := func(name string, value uint64) []byte {
		b := pbField(nil, 1, []byte(name))
		b = binary.AppendUvarint(b, 2<<3) // value = 2, varint
		return binary.AppendUvarint(b, value)
	}
	var answer []byte
	for _, s := range [][]byte{
		stat("user>>>u1>>>traffic>>>uplink", 100),
		stat("user>>>u1>>>traffic>>>downlink", 1<<40),
		stat("user>>>u2>>>traffic>>>downlink", 5),
		stat("inbound>>>vless-in>>>traffic>>>uplink", 9),
	} {
		answer = pbField(answer, 1, s)
	}
	var gotPattern string
	addr := serveH2C(t, "127.0.0.1:0", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != statsQueryStats || len(body) < 5 {
			http.Error(w, "not QueryStats", http.StatusBadRequest)
			return
		}
		if _, v, _, err := pbNext(body[5:]); err == nil {
			gotPattern = string(v)
		}
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		frame := make([]byte, 5, 5+len(answer))
		binary.BigEndian.PutUint32(frame[1:], uint32(len(answer)))
		_, _ = w.Write(append(frame, answer...))
		w.Header().Set("Grpc-Status", "0")
	})
	sup := NewSupervisor("", filepath.Join(t.TempDir(), "config.json"), t.TempDir())
	got, err := sup.QueryStatsLive(addr)
	if err != nil {
		t.Fatal(err)
	}
	if gotPattern != "user>>>" {
		t.Errorf("asked for %q, want every user counter", gotPattern)
	}
	want := map[string]Traffic{"u1": {Up: 100, Down: 1 << 40}, "u2": {Down: 5}}
	if len(got) != len(want) || got["u1"] != want["u1"] || got["u2"] != want["u2"] {
		t.Fatalf("got %v, want %v", got, want)
	}
}
