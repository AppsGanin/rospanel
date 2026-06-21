package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/core"
)

// xrayStatus is a lightweight check the UI polls to detect when a config change
// has finished restarting Xray (started_at advances on each reload).
func (rt *Router) xrayStatus(w http.ResponseWriter, _ *http.Request) {
	running, startedAt := rt.mgr.XrayStatus()
	writeJSON(w, http.StatusOK, map[string]any{"running": running, "started_at": startedAt})
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

	send := func() bool {
		s, err := rt.mgr.SystemStatus()
		if err != nil {
			return true // skip this tick, keep the stream open
		}
		b, err := json.Marshal(s)
		if err != nil {
			return true
		}
		return sseSend(w, flusher, string(b))
	}

	if !send() {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send() {
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
