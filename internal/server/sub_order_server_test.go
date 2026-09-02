package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A full master that is the only server still serves its subscription — hiding
// the last server would strand every client — and the nodes view carries the live
// online count and placement the operator set.
func TestSubscriptionNeverEmptiesOnLoadAndViewsShowOnline(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "loaded", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetMasterPlacement(model.Placement{Country: "nl", Capacity: 1, HideWhenFull: true}); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	set.SubOrderMode = model.OrderNearestLoad
	if err := mgr.SaveSubSettings(set); err != nil {
		t.Fatal(err)
	}
	// Two users online on the master: over its capacity of one.
	other, _ := mgr.CreateUser(t.Context(), "other", 0, 0)
	mgr.RecordLocalAccess(model.UserEmail(u.ID), "198.51.100.1", "")
	mgr.RecordLocalAccess(model.UserEmail(other.ID), "198.51.100.2", "")

	rec := fetchSubUA(h, u.SubToken, "v2rayNG/1.9")
	body := rec.Body.String()
	if decoded, err := base64.StdEncoding.DecodeString(body); err == nil {
		body = string(decoded) // the default link list is base64-wrapped
	}
	if rec.Code != http.StatusOK || !strings.Contains(body, u.UUID) {
		t.Fatalf("the only server must still be served when full: %d %q", rec.Code, body)
	}

	// The nodes view reports the count and the placement, under the JSON names the
	// panel reads.
	views, err := mgr.NodeViews()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) == 0 || views[0].ID != model.LocalNodeID {
		t.Fatalf("views: %+v", views)
	}
	raw, _ := json.Marshal(views[0])
	var v struct {
		Country      string `json:"country"`
		Capacity     int    `json:"capacity"`
		HideWhenFull bool   `json:"hide_when_full"`
		OnlineUsers  int    `json:"online_users"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Country != "NL" || v.Capacity != 1 || !v.HideWhenFull || v.OnlineUsers != 2 {
		t.Errorf("master view: %+v (%s)", v, raw)
	}
}
