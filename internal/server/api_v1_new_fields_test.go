package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/extsub"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

func apiDo(t *testing.T, h http.Handler, method, target, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The settings the panel's screens gained reach the external API too: the random
// server order, the encrypted Happ link and the trusted networks — read back in the
// form they were stored, applied only when sent, and refused before anything lands.
func TestAPISettingsCarryTheNewFields(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)

	rec := apiDo(t, h, http.MethodPatch, base+"/v1/settings", key,
		`{"sub_order_mode":"random","sub_happ_crypt":true,"trusted_nets":["198.51.100.7/24"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data apiSettingsView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.SubOrderMode != model.OrderRandom || !got.Data.SubHappCrypt ||
		strings.Join(got.Data.TrustedNets, ",") != "198.51.100.0/24" {
		t.Errorf("answer: %+v", got.Data)
	}
	set, _ := st.GetSettings()
	if set.SubOrderMode != model.OrderRandom || !set.SubHappCrypt || !mgr.Trusted("198.51.100.200") {
		t.Errorf("stored: order %q happ %v trusted %v", set.SubOrderMode, set.SubHappCrypt, mgr.Trusted("198.51.100.200"))
	}

	// A field left out stays; an unrelated patch does not reset the subscription row.
	if rec := apiDo(t, h, http.MethodPatch, base+"/v1/settings", key, `{"user_autodelete_days":3}`); rec.Code != http.StatusOK {
		t.Fatalf("unrelated patch: %d", rec.Code)
	}
	if set, _ := st.GetSettings(); set.SubOrderMode != model.OrderRandom || !set.SubHappCrypt || len(mgr.TrustedNets()) != 1 {
		t.Errorf("an unrelated patch changed the new fields: order %q happ %v trusted %v", set.SubOrderMode, set.SubHappCrypt, mgr.TrustedNets())
	}

	// Refused up front: nothing else in the body lands.
	for _, body := range []string{
		`{"user_autodelete_days":9,"sub_order_mode":"fastest"}`,
		`{"user_autodelete_days":9,"trusted_nets":["0.0.0.0/0"]}`,
	} {
		if rec := apiDo(t, h, http.MethodPatch, base+"/v1/settings", key, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d — want 400", body, rec.Code)
		}
		if set, _ := st.GetSettings(); set.UserAutoDeleteDays == 9 {
			t.Errorf("%s: a refused patch still wrote the field ahead of the bad one", body)
		}
	}
}

// The user's encrypted Happ link, and a server's placement — the master's included,
// which PATCH /v1/nodes cannot reach.
func TestAPIHappLinkAndMasterPlacement(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	u, err := mgr.CreateUser(t.Context(), "api-happ", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	link := func() string {
		t.Helper()
		rec := apiDo(t, h, http.MethodGet, fmt.Sprintf("%s/v1/users/%d/happ-link", base, u.ID), key, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("happ-link: %d %s", rec.Code, rec.Body.String())
		}
		var got struct {
			Data apiHappLinkResp `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		return got.Data.Link
	}
	if l := link(); l != "" {
		t.Errorf("with encrypted links off the API handed out %q", l)
	}
	if rec := apiDo(t, h, http.MethodPatch, base+"/v1/settings", key, `{"sub_happ_crypt":true}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	set, _ := st.GetSettings()
	if plain, err := extsub.DecryptHapp(link()); err != nil || string(plain) != sub.URL(set, u.SubToken) {
		t.Errorf("the API's Happ link opens %q (%v), want the user's subscription", plain, err)
	}
	if rec := apiDo(t, h, http.MethodGet, base+"/v1/users/999999/happ-link", key, ""); rec.Code != http.StatusNotFound {
		t.Errorf("a missing user: %d — want 404", rec.Code)
	}

	rec := apiDo(t, h, http.MethodPost, base+"/v1/nodes/0/placement", key,
		`{"traffic_limit":1099511627776,"traffic_reset_day":14,"country":"nl"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("master placement: %d %s", rec.Code, rec.Body.String())
	}
	var placed struct {
		Data model.Placement `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &placed)
	if placed.Data.TrafficResetDay != 14 || placed.Data.Country != "NL" {
		t.Errorf("answer: %+v", placed.Data)
	}
	// Partial: a later edit of the weight keeps the cap and its reset day.
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/nodes/0/placement", key, `{"sort_weight":5}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if set, _ := st.GetSettings(); set.MasterPlacement.TrafficResetDay != 14 || set.MasterPlacement.Weight != 5 ||
		set.MasterPlacement.TrafficLimit != 1<<40 {
		t.Errorf("stored master placement: %+v", set.MasterPlacement)
	}
	if rec := apiDo(t, h, http.MethodPost, base+"/v1/nodes/0/placement", key, `{"traffic_reset_day":40}`); rec.Code != http.StatusBadRequest {
		t.Errorf("reset day 40: %d — want 400", rec.Code)
	}

	n, err := st.CreateNode("api-node", "api-node.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if rec := apiDo(t, h, http.MethodPost, fmt.Sprintf("%s/v1/nodes/%d/placement", base, n.ID), key,
		`{"traffic_limit":5368709120,"traffic_reset_day":3}`); rec.Code != http.StatusOK {
		t.Fatalf("node placement: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := st.GetNode(n.ID); got.TrafficResetDay != 3 || got.Name != "api-node" {
		t.Errorf("stored node: reset %d name %q", got.TrafficResetDay, got.Name)
	}
}
