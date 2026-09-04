package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

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

// The external servers are the panel's, not the master's. A full master with
// hide-when-full drops out of the ordering like any other server, and while it was
// the only entry allowed to carry them, it took every external server with it —
// silently, and for a plan whose grants are all external that is the entire
// subscription. A surviving node is what makes this visible: with the master alone,
// Order's "never empty the list" rescue keeps it and the loss cannot happen.
func TestExternalServersOutliveAHiddenFullMaster(t *testing.T) {
	const extLink = "vless://11111111-2222-3333-4444-555555555555@9.9.9.9:443" +
		"?type=tcp&security=tls&sni=partner.example#Partner"

	h, mgr, st := nodeAPITestServer(t)
	if _, _, err := mgr.CreateExtSubscription(t.Context(), "partner", extLink); err != nil {
		t.Fatal(err)
	}

	// A node that has installed (a reported fingerprint is what makes it linkable)
	// and synced just now, so it survives the ordering the master falls out of.
	node, err := mgr.CreateNode("live", "live.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNodeStatus(node.ID, model.NodeStatusUpdate{
		LastSeen: time.Now().Unix(), NodeVersion: "test", XrayRunning: true,
		CertSHA256: "aa", CertSelfSigned: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Master: capacity of one, hide when full, two users online.
	if err := mgr.SetMasterPlacement(model.Placement{Capacity: 1, HideWhenFull: true}); err != nil {
		t.Fatal(err)
	}
	u, err := mgr.CreateUser(t.Context(), "ext-user", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := mgr.CreateUser(t.Context(), "other", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	mgr.RecordLocalAccess(model.UserEmail(u.ID), "198.51.100.1", "")
	mgr.RecordLocalAccess(model.UserEmail(other.ID), "198.51.100.2", "")

	rec := fetchSubUA(h, u.SubToken, "v2rayNG/1.9")
	body := rec.Body.String()
	if decoded, err := base64.StdEncoding.DecodeString(body); err == nil {
		body = string(decoded)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %q", rec.Code, body)
	}
	// The master really is hidden — otherwise this proves nothing, since the master
	// would simply be carrying the externals as it always did.
	if strings.Contains(body, u.UUID) {
		t.Fatalf("the master was expected to drop out as full: %q", body)
	}
	if !strings.Contains(body, "9.9.9.9") {
		t.Errorf("external server lost with the hidden master: %q", body)
	}
}
