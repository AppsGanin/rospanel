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
	rec := &captureWriter{header: http.Header{}}
	rt.apiInner.ServeHTTP(rec, r.WithContext(actx))
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.status, rec.body.Bytes(), nil
}

// SetPlugins hands the router the plugin host. Called once, before serving.
func (rt *Router) SetPlugins(h *plugin.Host) { rt.plugins = h }
