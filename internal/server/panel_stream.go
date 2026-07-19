package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
)

// xrayStatus is a lightweight check the UI polls to detect when a config change
// has finished restarting Xray (started_at advances on each reload).
func (rt *Router) xrayStatus(w http.ResponseWriter, _ *http.Request) {
	running, startedAt := rt.mgr.XrayStatus()
	writeJSON(w, http.StatusOK, map[string]any{"running": running, "started_at": startedAt})
}

// xrayRestart bounces the Xray child from the config on disk. It drops every live
// VPN connection, so the UI confirms first. The response carries the new
// started_at, which the dashboard already polls to tell a reload apart from a
// no-op.
func (rt *Router) xrayRestart(w http.ResponseWriter, _ *http.Request) {
	if err := rt.mgr.RestartXray(); err != nil {
		slog.Error("xray: restart requested by operator failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "не удалось перезапустить Xray")
		return
	}
	running, startedAt := rt.mgr.XrayStatus()
	slog.Info("xray: restarted by operator")
	writeJSON(w, http.StatusOK, map[string]any{"running": running, "started_at": startedAt})
}

// statusInterval is how often the dashboard payload is recomputed — once for the
// whole panel, not once per viewer.
const statusInterval = 2 * time.Second

// statusFeed computes the dashboard payload on one timer and hands the same
// marshalled JSON to every open stream.
//
// It exists because the work behind SystemStatus is not free — it counts users and
// sums traffic in the database — and it used to run per connected admin, per tick.
// Two open browser tabs meant twice the queries for byte-identical output, all of
// it queued through the single write connection the rest of the panel shares. Now
// the cost is fixed no matter how many people have the dashboard open.
//
// The timer only runs while somebody is watching: it starts with the first
// subscriber and stops with the last, so an idle panel does no polling at all.
type statusFeed struct {
	payload  func() (any, error)
	interval time.Duration

	mu   sync.Mutex
	subs map[chan string]struct{}
	last string        // most recent payload, so a new tab paints immediately
	stop chan struct{} // closed when the last subscriber leaves
}

func newStatusFeed(mgr *core.Manager) *statusFeed {
	return newStatusFeedFunc(statusInterval, func() (any, error) { return mgr.SystemStatus() })
}

// newStatusFeedFunc builds a feed over any payload source, so the fan-out can be
// tested without standing up a Manager.
func newStatusFeedFunc(interval time.Duration, payload func() (any, error)) *statusFeed {
	return &statusFeed{
		payload:  payload,
		interval: interval,
		subs:     make(map[chan string]struct{}),
	}
}

// subscribe registers a stream and returns its channel and a release func. The
// channel is closed by release; readers must handle that.
func (f *statusFeed) subscribe() (<-chan string, func()) {
	ch := make(chan string, 1)
	f.mu.Lock()
	f.subs[ch] = struct{}{}
	if f.last != "" {
		ch <- f.last // don't make a fresh tab wait a tick for its first paint
	}
	if len(f.subs) == 1 {
		f.stop = make(chan struct{})
		go f.loop(f.stop)
	}
	f.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			delete(f.subs, ch)
			close(ch)
			if len(f.subs) == 0 {
				if f.stop != nil {
					close(f.stop)
					f.stop = nil
				}
				// Drop the cached payload with the last viewer. It only exists to spare a
				// new tab the wait for the next tick; once nothing is refreshing it, it
				// would instead hand that tab a snapshot from whenever the panel was last
				// watched — and since the channel buffers one message, the fresh payload
				// published moments later is dropped, leaving the stale numbers up.
				f.last = ""
			}
		})
	}
}

func (f *statusFeed) loop(stop chan struct{}) {
	t := time.NewTicker(f.interval)
	defer t.Stop()
	for {
		f.publish()
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (f *statusFeed) publish() {
	s, err := f.payload()
	if err != nil {
		return // skip this tick; the streams stay open
	}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	msg := string(b)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = msg
	for ch := range f.subs {
		select {
		case ch <- msg:
		default:
			// Reader is behind. Drop this tick for them rather than stall the
			// publisher (and every other viewer) — the next one is 2s away.
		}
	}
}

// systemStream pushes the dashboard payload over Server-Sent Events every 2s, so
// the client subscribes once instead of polling. EventSource reconnects on its
// own if the stream drops (e.g. an Xray reload bounces the connection).
func (rt *Router) systemStream(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !rt.streams.acquire(ip) {
		writeErr(w, http.StatusTooManyRequests, "слишком много активных потоков")
		return
	}
	defer rt.streams.release(ip)
	flusher, ok := sseStart(w)
	if !ok {
		return
	}
	// While this stream is open, the live-throughput sampler runs; it idles (no
	// xray fork every 3s) when no dashboard is watching.
	defer rt.mgr.TrackVPNViewer()()

	ch, release := rt.status.subscribe()
	defer release()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if !sseSend(w, flusher, msg) {
				return
			}
		}
	}
}

// xrayConfig returns the live on-disk Xray config.json (pretty-printed).
func (rt *Router) xrayConfig(w http.ResponseWriter, _ *http.Request) {
	raw, err := rt.mgr.XrayConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "конфиг недоступен: "+err.Error())
		return
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		raw = pretty.Bytes()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(raw)
}

// xrayLogs streams Xray log lines (access + error) over SSE: the buffered tail
// first, then new lines live as they arrive.
func (rt *Router) xrayLogs(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !rt.streams.acquire(ip) {
		writeErr(w, http.StatusTooManyRequests, "слишком много активных потоков")
		return
	}
	defer rt.streams.release(ip)
	flusher, ok := sseStart(w)
	if !ok {
		return
	}

	// One SSE event per line; \r is stripped so the frame stays single-line.
	writeLine := func(line string) bool {
		return sseSend(w, flusher, strings.TrimRight(line, "\r"))
	}

	ch, unsub := rt.mgr.SubscribeXrayLogs()
	defer unsub()

	for _, line := range rt.mgr.XrayLogTail() {
		if !writeLine(line) {
			return
		}
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			if !writeLine(line) {
				return
			}
		}
	}
}

// appLogs streams the panel's own log lines (everything written to the standard
// logger) over SSE: the buffered tail first, then new lines live.
func (rt *Router) appLogs(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !rt.streams.acquire(ip) {
		writeErr(w, http.StatusTooManyRequests, "слишком много активных потоков")
		return
	}
	defer rt.streams.release(ip)
	flusher, ok := sseStart(w)
	if !ok {
		return
	}
	writeLine := func(line string) bool {
		return sseSend(w, flusher, strings.TrimRight(line, "\r"))
	}

	ch, unsub := rt.mgr.SubscribeAppLogs()
	defer unsub()

	for _, line := range rt.mgr.AppLogTail() {
		if !writeLine(line) {
			return
		}
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			if !writeLine(line) {
				return
			}
		}
	}
}

func (rt *Router) connections(w http.ResponseWriter, _ *http.Request) {
	c, err := rt.mgr.ConnectionsInfo()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// applyConnections persists the whole connection surface (protocol toggles,
// fingerprint, WS path, Hysteria2 ports/interval) in one shot and reconciles once.
func (rt *Router) applyConnections(w http.ResponseWriter, r *http.Request) {
	var req core.ConnectionsUpdate
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.mgr.ApplyConnections(req); err != nil {
		writeManagerErr(w, err)
		return
	}
	c, err := rt.mgr.ConnectionsInfo()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
