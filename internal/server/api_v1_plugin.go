package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin"
)

// PluginAPI serves a plugin's panel.api call: the request runs through the same /v1
// handlers an external client reaches — validation, permissions, audit, the users
// list's write tracking — with the plugin's granted permissions in place of a key's
// and the plugin as the actor in the audit log. Like mcpDispatch, nothing about an
// endpoint is written twice.
func (rt *Router) PluginAPI(ctx context.Context, req plugin.APIRequest) (int, []byte, error) {
	if rt.apiInner == nil {
		return 0, nil, errors.New("the API is not set up")
	}
	if req.ReadOnly && req.Method != http.MethodGet {
		return 0, nil, errors.New("only GET inside a decision hook")
	}
	var body io.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}
	r, err := http.NewRequestWithContext(ctx, req.Method, req.Path, body)
	if err != nil {
		return 0, nil, err
	}
	if req.Body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	// RemoteAddr stays empty, as for MCP: an in-process caller has no address, and
	// nothing may read a made-up one (127.0.0.1 would look like the box itself).
	actx := actor.With(r.Context(), actor.Plugin(req.Plugin))
	actx = withAPIAccess(actx, apiAccess{perms: model.NewPermSet(req.Perms)})
	if req.Sink != nil {
		sw := &sinkWriter{header: http.Header{}, sink: req.Sink}
		rt.apiInner.ServeHTTP(sw, r.WithContext(actx))
		if sw.err != nil {
			return 0, nil, sw.err
		}
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		return sw.status, nil, nil
	}
	rec := &captureWriter{header: http.Header{}}
	rt.apiInner.ServeHTTP(rec, r.WithContext(actx))
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.status, rec.body.Bytes(), nil
}

// sinkWriter streams an answer into a plugin's blob (a backup is no answer to hold
// in memory); the first write error is kept for the caller.
type sinkWriter struct {
	header http.Header
	sink   io.Writer
	status int
	err    error
}

func (s *sinkWriter) Header() http.Header { return s.header }

func (s *sinkWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	if s.err != nil {
		return 0, s.err
	}
	n, err := s.sink.Write(b)
	if err != nil {
		s.err = err
	}
	return n, err
}

func (s *sinkWriter) WriteHeader(status int) {
	if s.status == 0 {
		s.status = status
	}
}

// Flush lets a streaming handler flush; the bytes are already in the blob.
func (s *sinkWriter) Flush() {}

// SetPlugins hands the router the plugin host. Called once, before serving.
func (rt *Router) SetPlugins(h *plugin.Host) { rt.plugins = h }
