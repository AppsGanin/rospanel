package h2fix

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// A held response over unencrypted HTTP/2 must outlive ReadHeaderTimeout. Without
// the wrapper net/http leaves that timeout armed as a raw read deadline for the
// life of the connection and tears the whole thing down mid-response — which is
// what killed the dashboard's SSE stream every ten seconds in production.
func TestHeldHTTP2ResponseOutlivesReadHeaderTimeout(t *testing.T) {
	const headerTimeout = 200 * time.Millisecond
	const hold = 5 * headerTimeout

	for _, tc := range []struct {
		name     string
		wrap     bool
		wantHeld bool
	}{
		{name: "wrapped", wrap: true, wantHeld: true},
		// The unwrapped case pins the net/http behaviour this package exists for: if
		// it ever starts holding, the wrapper has become dead weight and can go.
		{name: "unwrapped", wrap: false, wantHeld: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			if tc.wrap {
				ln = Listener{Listener: ln}
			}

			protocols := new(http.Protocols)
			protocols.SetHTTP1(true)
			protocols.SetUnencryptedHTTP2(true)
			srv := &http.Server{
				Protocols:         protocols,
				ReadHeaderTimeout: headerTimeout,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// A stand-in for the SSE stream: a frame now, another after the hold.
					fmt.Fprint(w, "open\n")
					w.(http.Flusher).Flush()
					select {
					case <-time.After(hold):
					case <-r.Context().Done():
						return
					}
					fmt.Fprint(w, "still here\n")
					w.(http.Flusher).Flush()
				}),
			}
			go func() { _ = srv.Serve(ln) }()
			t.Cleanup(func() { _ = srv.Close() })

			client := &http.Client{Transport: h2cTransport()}
			ctx, cancel := context.WithTimeout(context.Background(), 4*hold)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ln.Addr().String()+"/", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()
			if resp.ProtoMajor != 2 {
				t.Fatalf("negotiated HTTP/%d, want HTTP/2 — the test is not exercising the h2 path", resp.ProtoMajor)
			}

			body, err := io.ReadAll(resp.Body)
			held := err == nil && len(body) > len("open\n")
			if held != tc.wantHeld {
				t.Fatalf("held=%v (body %q, err %v), want held=%v", held, body, err, tc.wantHeld)
			}
		})
	}
}

// h2cTransport speaks HTTP/2 over a plaintext connection (prior knowledge), the
// way Xray's fallback hands a browser's h2 connection to the panel.
func h2cTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Protocols = new(http.Protocols)
	tr.Protocols.SetUnencryptedHTTP2(true)
	return tr
}
