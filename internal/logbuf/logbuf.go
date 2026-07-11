// Package logbuf captures the panel's own log output into an in-memory ring
// buffer and fans new lines out to live subscribers (the dashboard log viewer).
// It implements io.Writer so it can be installed as a tee on the standard
// logger — every log.Printf across the app is then both written to stderr (for
// journald) and made available to the panel UI.
package logbuf

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// bufferSize caps the in-memory ring shown to newly-opened viewers.
const bufferSize = 1000

// loc is the timezone log timestamps are rendered in. It lives here because the
// log handler is installed in main() before the store exists, while the zone only
// becomes known once settings load — so the handler reads it through this atomic
// and core updates it on boot and whenever the operator changes the setting.
// Without it, logs render in the SERVER's system zone while every other panel
// surface uses the operator's, and the two disagree (e.g. Berlin vs Moscow).
var loc atomic.Pointer[time.Location]

// SetLocation sets the timezone used for log timestamps.
func SetLocation(l *time.Location) {
	if l != nil {
		loc.Store(l)
	}
}

// Location returns the log timezone, defaulting to server-local until one is set.
func Location() *time.Location {
	if l := loc.Load(); l != nil {
		return l
	}
	return time.Local
}

// Hub keeps a ring of recent log lines and broadcasts new ones to subscribers.
type Hub struct {
	mu   sync.Mutex
	buf  []string
	subs map[chan string]struct{}
}

// Default is the process-wide hub the standard logger tees into.
var Default = New()

// New builds an empty hub.
func New() *Hub {
	return &Hub{subs: make(map[chan string]struct{})}
}

// Write implements io.Writer: it splits the written bytes into lines, appends
// them to the ring, and broadcasts each to live subscribers. It always reports
// the full length consumed so it composes cleanly inside an io.MultiWriter.
func (h *Hub) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\n")
	if text == "" {
		return len(p), nil
	}
	h.mu.Lock()
	for _, line := range strings.Split(text, "\n") {
		h.buf = append(h.buf, line)
		if len(h.buf) > bufferSize {
			h.buf = h.buf[len(h.buf)-bufferSize:]
		}
		for ch := range h.subs {
			select {
			case ch <- line:
			default: // drop for a slow subscriber rather than block the logger
			}
		}
	}
	h.mu.Unlock()
	return len(p), nil
}

// Tail returns a copy of the buffered recent log lines.
func (h *Hub) Tail() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.buf...)
}

// Subscribe returns a channel of new log lines and an unsubscribe func.
func (h *Hub) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}
