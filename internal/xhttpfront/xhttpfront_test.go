package xhttpfront

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/proxyproto"
)

// upstream is a stand-in for Xray's XHTTP inbound: h2c and HTTP/1.1 on loopback, a
// PROXY line required in front of every connection.
type upstream struct {
	addr string
	mu   sync.Mutex
	seen []*http.Request
	conn map[string]int // client address (from the PROXY line) → upstream connections
}

func newUpstream(t *testing.T, h http.HandlerFunc) *upstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u := &upstream{addr: ln.Addr().String(), conn: map[string]int{}}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{
		Protocols: protocols,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u.mu.Lock()
			u.seen = append(u.seen, r)
			u.mu.Unlock()
			h(w, r)
		}),
		ConnState: func(c net.Conn, s http.ConnState) {
			if s == http.StateNew {
				u.mu.Lock()
				u.conn[c.RemoteAddr().String()]++
				u.mu.Unlock()
			}
		},
	}
	go func() { _ = srv.Serve(&proxyproto.Listener{Listener: ln}) }()
	t.Cleanup(func() { _ = srv.Close() })
	return u
}

func (u *upstream) last() *http.Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.seen) == 0 {
		return nil
	}
	return u.seen[len(u.seen)-1]
}

// front serves the router in front of a decoy, the way the panel and the node agent do
// behind Xray's fallback: h2c and HTTP/1.1, the client's address taken from the
// listener (here, the test client's own).
func front(t *testing.T, r *Router) *httptest.Server {
	t.Helper()
	decoy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "Caddy")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "decoy")
	})
	srv := httptest.NewUnstartedServer(r.Wrap(decoy))
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func h2cClient() *http.Client {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	return &http.Client{Transport: &http.Transport{Protocols: p}, Timeout: 10 * time.Second}
}

func TestRoutesByPathAndLeavesTheRestToTheDecoy(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "xhttp") })
	r := New()
	r.SetRoutes([]Route{{Path: "/secretpath1/", Addr: up.addr}, {Path: "/", Addr: up.addr}})
	if got := len(r.Routes()); got != 1 {
		t.Fatalf("routes = %d, want 1 (\"/\" must never be routed)", got)
	}
	srv := front(t, r)
	c := h2cClient()

	for path, want := range map[string]string{
		"/secretpath1":            "xhttp",
		"/secretpath1/":           "xhttp",
		"/secretpath1/sess/0":     "xhttp",
		"/secretpath1x":           "decoy", // a longer name is another path
		"/":                       "decoy",
		"/other/secretpath1/sess": "decoy",
	} {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != want {
			t.Errorf("%s: got %q, want %q", path, body, want)
		}
		if s := resp.Header.Get("Server"); s != "Caddy" {
			t.Errorf("%s: Server = %q, want Caddy on both sides", path, s)
		}
	}
}

func TestUpstreamSeesTheClientNotForgedHeaders(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {})
	r := New()
	r.SetRoutes([]Route{{Path: "/secretpath1", Addr: up.addr}})
	srv := front(t, r)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/secretpath1/s/1", strings.NewReader("data"))
	req.Host = "vpn.example.com"
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("X-Real-Ip", "6.6.6.6")
	req.Header.Set("Forwarded", "for=6.6.6.6")
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := req.Write(conn); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	got := up.last()
	if got == nil {
		t.Fatal("the request never reached the inbound")
	}
	if got.RemoteAddr != conn.LocalAddr().String() {
		t.Errorf("inbound saw %s, want the client %s (from the PROXY line)", got.RemoteAddr, conn.LocalAddr())
	}
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
		if v := got.Header.Get(h); v != "" {
			t.Errorf("%s reached the inbound: %q", h, v)
		}
	}
	if got.Host != "vpn.example.com" {
		t.Errorf("Host = %q, want the client's", got.Host)
	}
}

// Each client connection gets its own upstream connection, so the PROXY line — and
// a cut by socket — is per client; two requests on one client connection share one.
func TestOneUpstreamConnectionPerClientConnection(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {})
	r := New()
	r.SetRoutes([]Route{{Path: "/secretpath1", Addr: up.addr}})
	srv := front(t, r)

	a, b := h2cClient(), h2cClient()
	for _, c := range []*http.Client{a, a, a, b, b} {
		resp, err := c.Get(srv.URL + "/secretpath1/s")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if len(up.conn) != 2 {
		t.Fatalf("upstream connections by client = %v, want 2 clients", up.conn)
	}
	for client, n := range up.conn {
		if n != 1 {
			t.Errorf("client %s got %d upstream connections, want 1", client, n)
		}
	}
}

// An XHTTP download is a stream: the client must see each chunk as the inbound sends
// it, while its upload is still open (stream-up over HTTP/2).
func TestStreamsBothWays(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		fl.Flush()
		br := bufio.NewReader(r.Body)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			_, _ = io.WriteString(w, "echo:"+line)
			fl.Flush()
		}
	})
	r := New()
	r.SetRoutes([]Route{{Path: "/secretpath1", Addr: up.addr}})
	srv := front(t, r)

	pr, pw := io.Pipe()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/secretpath1/s", pr)
	resp, err := h2cClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	for _, msg := range []string{"one\n", "two\n"} {
		if _, err := io.WriteString(pw, msg); err != nil {
			t.Fatal(err)
		}
		got, err := br.ReadString('\n')
		if err != nil || got != "echo:"+msg {
			t.Fatalf("got %q, %v; want the echo of %q before the upload ends", got, err, msg)
		}
	}
	_ = pw.Close()
}

func TestUpstreamHostRoundTrips(t *testing.T) {
	for _, c := range [][2]string{
		{"@rospanel-xhttp-12", "[2001:db8::1]:65535"},
		{"127.0.0.1:18512", "203.0.113.7:40000"},
		{"@x", ""},
	} {
		addr, remote, ok := parseUpstreamHost(upstreamHost(c[0], c[1]))
		if !ok || addr != c[0] || remote != c[1] {
			t.Errorf("%v: got %q %q %v", c, addr, remote, ok)
		}
	}
	if _, _, ok := parseUpstreamHost("example.com"); ok {
		t.Error("a host that is not ours parsed")
	}
}
