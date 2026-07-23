package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The node config viewer must show the config the node actually runs — which the
// panel generates for it — and must not fall back to the master's own file when
// asked for a node. Getting that wrong shows the operator a config from the wrong
// server, which is worse than showing nothing.
func TestNodeXrayConfigServesTheGeneratedConfig(t *testing.T) {
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "admin", model.RoleAdmin, false)
	// First-run bootstrap seeds a WS path; this bare store has none and config
	// generation requires one.
	if err := st.SetWSPath("/ws"); err != nil {
		t.Fatalf("seed ws path: %v", err)
	}

	node, err := rt.mgr.CreateNode("berlin", "de.example.com")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/nodes/"+strconv.FormatInt(node.ID, 10)+"/xray-config", nil)
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET node config = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var cfg struct {
		Inbounds []struct {
			Listen   string `json:"listen"`
			Settings struct {
				Clients []json.RawMessage `json:"clients"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("body is not an Xray config: %v\n%s", err, rec.Body.String())
	}
	if len(cfg.Inbounds) == 0 {
		t.Error("generated config has no inbounds — the viewer would show an empty server")
	}

	// A node that doesn't exist is a bad request, not someone else's config.
	if got := call(h, "GET", "/api/nodes/999/xray-config", admin); got != http.StatusBadRequest {
		t.Errorf("GET config of a missing node = %d, want 400", got)
	}
	if got := call(h, "GET", "/api/nodes/"+strconv.FormatInt(node.ID, 10)+"/xray-config", nil); got != http.StatusUnauthorized {
		t.Errorf("GET node config anonymously = %d, want 401", got)
	}
}
