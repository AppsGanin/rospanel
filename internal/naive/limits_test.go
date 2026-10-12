package naive

import (
	"io"
	"net/http"
	"testing"
	"time"
)

func waitOpen(t *testing.T, s *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for s.Open() != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := s.Open(); got != want {
		t.Fatalf("%d tunnels open, want %d", got, want)
	}
}

// A user past maxStreamsPerUser gets no further tunnel, and gets one again once a
// stream closes.
func TestStreamsPerUserAreCapped(t *testing.T) {
	defer func(n int) { maxStreamsPerUser = n }(maxStreamsPerUser)
	maxStreamsPerUser = 3
	_, s, addr := uotLane(t)
	var bodies []io.Closer
	for range 3 {
		pr, pw := io.Pipe()
		resp := connect(t, addr, testEmail+":"+testUUID, uotMagicLegacy+":0", pr, nil) //nolint:bodyclose // closed below
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream refused under the cap: %d", resp.StatusCode)
		}
		bodies = append(bodies, resp.Body, pw)
	}
	over := connect(t, addr, testEmail+":"+testUUID, uotMagicLegacy+":0", http.NoBody, nil)
	over.Body.Close()
	if over.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stream past the cap: %d", over.StatusCode)
	}
	for _, c := range bodies {
		c.Close()
	}
	waitOpen(t, s, 0)
	again := connect(t, addr, testEmail+":"+testUUID, "example.com:443", http.NoBody, map[string]string{"Padding-Type-Request": "0"})
	again.Body.Close()
	if again.StatusCode != http.StatusOK {
		t.Fatalf("no stream after the others closed: %d", again.StatusCode)
	}
}

// A UDP tunnel the client sends nothing on ends after udpIdle.
func TestIdleUDPTunnelEnds(t *testing.T) {
	defer func(d time.Duration) { udpIdle = d }(udpIdle)
	udpIdle = 200 * time.Millisecond
	_, s, addr := uotLane(t)
	pr, pw := io.Pipe()
	defer pw.Close()
	resp := connect(t, addr, testEmail+":"+testUUID, uotMagicLegacy+":0", pr, nil) //nolint:bodyclose // closed below
	defer resp.Body.Close()
	waitOpen(t, s, 1)
	waitOpen(t, s, 0)
}

// A quiet TCP tunnel outlives writeTimeout: the deadline covers a write, not the gaps.
func TestQuietTunnelOutlivesTheWriteTimeout(t *testing.T) {
	defer func(d time.Duration) { writeTimeout = d }(writeTimeout)
	writeTimeout = 200 * time.Millisecond
	fx := newFakeXray(t)
	s := New(fx.ln.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	addr := startLane(t, s)
	pr, pw := io.Pipe()
	defer pw.Close()
	resp := connect(t, addr, testEmail+":"+testUUID, "example.com:443", pr, map[string]string{"Padding-Type-Request": "0"}) //nolint:bodyclose // closed below
	defer resp.Body.Close()
	for i := range 2 {
		go func() { _, _ = pw.Write([]byte("x")) }()
		if _, err := io.ReadFull(resp.Body, make([]byte, 1)); err != nil {
			t.Fatalf("echo %d: %v", i, err)
		}
		time.Sleep(3 * writeTimeout)
	}
}

// A client that stops reading has its tunnel reset after writeTimeout, its place in the
// user's count given back, even though nobody removed the user.
func TestStalledClientIsReset(t *testing.T) {
	defer func(d time.Duration) { writeTimeout = d }(writeTimeout)
	writeTimeout = 300 * time.Millisecond
	src := listen(t)
	go func() {
		c, err := src.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 1+16+1+1+2+1+1+11)
		if _, err := io.ReadFull(c, make([]byte, len("PROXY TCP4 127.0.0.1 127.0.0.1 00000 443\r\n"))); err != nil {
			return
		}
		_, _ = io.ReadFull(c, buf)
		_, _ = c.Write([]byte{0, 0})
		_, _ = io.CopyN(c, zeroReader{}, 64<<20) // far more than any window holds
	}()
	s := New(src.Addr().String())
	s.SetUsers("", []User{{Email: testEmail, UUID: testUUID}})
	addr := startLane(t, s)
	pr, pw := io.Pipe()
	defer pw.Close()
	resp := connect(t, addr, testEmail+":"+testUUID, "example.com:443", pr, map[string]string{"Padding-Type-Request": "0"}) //nolint:bodyclose // closed below
	defer resp.Body.Close()
	if _, err := io.ReadFull(resp.Body, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	waitOpen(t, s, 0)
	s.mu.Lock()
	held, streams := s.perUser[testEmail], s.userStreams[testEmail]
	s.mu.Unlock()
	if held != 0 || streams != 0 {
		t.Fatalf("places still held: %d connections, %d streams", held, streams)
	}
}
