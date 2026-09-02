package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
)

// With the lane on, the subscription page offers the AmneziaWG card and the
// config/QR endpoints hand out this user's config for the master; with the lane
// off — or for a server that does not exist — the endpoints are the decoy.
func TestAWGConfigEndpointsAndPageCard(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "awg", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path, ua string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", ua)
		if strings.HasPrefix(ua, "Mozilla") { // a browser asks for the page, not the payload
			req.Header.Set("Accept", "text/html,application/xhtml+xml")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	base := "/sub/" + u.SubToken

	// Off: the page has no card, the endpoints answer with the decoy (not our text).
	if body := get(base, "Mozilla/5.0").Body.String(); strings.Contains(body, "/awg/0.conf") {
		t.Error("page offers an AmneziaWG config with the lane off")
	}
	if body := get(base+"/awg/0.conf", "curl/8").Body.String(); strings.Contains(body, "[Interface]") {
		t.Error("config served with the lane off")
	}

	// On, through the same path the Connections panel uses.
	status, err := mgr.ConnectionsInfo()
	if err != nil {
		t.Fatal(err)
	}
	upd := core.ConnectionsUpdate{
		Protocols:    map[string]bool{"vless": true, "reality": false, "hysteria2": false, "awg": true},
		Names:        map[string]string{"awg": "Amnezia NL"},
		HysteriaPort: status.HysteriaPort, HopStart: status.HopStart, HopEnd: status.HopEnd,
		HopInterval: status.HopInterval, RealityPort: status.RealityPort, RealityDest: status.RealityDest,
		AWGPort: 40123, AWGDNS: "9.9.9.9",
	}
	if err := mgr.ApplyConnections(upd); err != nil {
		t.Fatalf("apply: %v", err)
	}
	set, _ := st.GetSettings()
	if !set.AWGEnabled || set.AWGPort != 40123 || set.AWGPublicKey == "" || set.AWGName != "Amnezia NL" || set.AWGDNS != "9.9.9.9" {
		t.Fatalf("settings after apply: on=%v port=%d pub=%q name=%q dns=%q", set.AWGEnabled, set.AWGPort, set.AWGPublicKey, set.AWGName, set.AWGDNS)
	}
	status, _ = mgr.ConnectionsInfo()
	var awgInfo *core.ConnInfo
	for i := range status.Protocols {
		if status.Protocols[i].Key == "awg" {
			awgInfo = &status.Protocols[i]
		}
	}
	if awgInfo == nil || !awgInfo.Enabled || awgInfo.Port != "40123" || awgInfo.DisplayName != "Amnezia NL" {
		t.Errorf("connections status: %+v", awgInfo)
	}

	page := get(base, "Mozilla/5.0").Body.String()
	if !strings.Contains(page, "/awg/0.conf") || !strings.Contains(page, "/awg/0.png") || !strings.Contains(page, "Amnezia NL") {
		t.Error("page lacks the AmneziaWG card")
	}
	conf := get(base+"/awg/0.conf", "curl/8")
	if conf.Code != http.StatusOK || !strings.Contains(conf.Body.String(), "[Interface]") ||
		!strings.Contains(conf.Body.String(), "PublicKey = "+set.AWGPublicKey) ||
		!strings.Contains(conf.Body.String(), ":40123") || !strings.Contains(conf.Body.String(), "DNS = 9.9.9.9") {
		t.Errorf("config: %d\n%s", conf.Code, conf.Body.String())
	}
	if cd := conf.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="Amnezia-NL.conf"`) {
		t.Errorf("file name: %q", cd)
	}
	if png := get(base+"/awg/0.png", "curl/8"); png.Code != http.StatusOK || png.Header().Get("Content-Type") != "image/png" {
		t.Errorf("qr: %d %q", png.Code, png.Header().Get("Content-Type"))
	}
	// A server that does not exist, or a bad name: the decoy.
	if body := get(base+"/awg/77.conf", "curl/8").Body.String(); strings.Contains(body, "[Interface]") {
		t.Error("config served for an unknown server")
	}
	if body := get(base+"/awg/x.conf", "curl/8").Body.String(); strings.Contains(body, "[Interface]") {
		t.Error("config served for a malformed id")
	}
	// The user card lists the config address as the lane's link.
	fresh, _ := st.GetUser(u.ID)
	view := makeUserView(*fresh, set, "", nil, nil, model.UnrestrictedAccess())
	found := false
	for _, l := range view.Links {
		if l.Name == "Amnezia NL" && strings.HasSuffix(l.URL, "/awg/0.conf") {
			found = true
		}
	}
	if !found {
		t.Errorf("user view lacks the AmneziaWG link: %+v", view.Links)
	}
	// Groups offer the lane.
	targets, err := mgr.GroupTargets()
	if err != nil {
		t.Fatal(err)
	}
	laneFound := false
	for _, tg := range targets {
		for _, l := range tg.Lanes {
			if l.Lane == model.LaneAWG && l.Enabled {
				laneFound = true
			}
		}
	}
	if !laneFound {
		t.Error("group targets do not offer the awg lane")
	}
}
