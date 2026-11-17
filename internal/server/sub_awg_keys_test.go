package server

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/core"
	"github.com/AppsGanin/rospanel/internal/model"
)

// A client that sends x-hwid and reads AmneziaWG gets a key of its own; the page's
// picker adds and deletes keys and writes the card's configs with the picked one.
func TestSubAWGDeviceKeys(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "awg", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	status, _ := mgr.ConnectionsInfo()
	if err := mgr.ApplyConnections(core.ConnectionsUpdate{
		Protocols:    map[string]bool{"vless": true, "reality": false, "hysteria2": false, "awg": true},
		HysteriaPort: status.HysteriaPort, HopStart: status.HopStart, HopEnd: status.HopEnd,
		HopInterval: status.HopInterval, RealityPort: status.RealityPort, RealityDest: status.RealityDest,
		AWGPort: 40123,
	}); err != nil {
		t.Fatal(err)
	}
	// A plain WireGuard inbound, made before anything reads the shared sub inputs.
	cur, _ := st.GetSettings()
	if err := st.SetTLSMode(cur.TLSMode, "vpn.example.com", "vpn.example.com", cur.ACMEEmail); err != nil {
		t.Fatal(err)
	}
	pc, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	pc.Close()
	wg, err := mgr.CreateInbound(t.Context(), model.Inbound{
		ServerID: model.LocalNodeID, Enabled: true, Name: "Plain WG", Protocol: model.InbWireGuard, Port: port,
	})
	if err != nil {
		t.Fatal(err)
	}
	do := func(req *http.Request) *httptest.ResponseRecorder {
		req.RemoteAddr = testClientIP + ":40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	base := "/sub/" + u.SubToken
	incy := func(hwid string) string {
		req := httptest.NewRequest(http.MethodGet, base, nil)
		req.Header.Set("User-Agent", "INCY/3.8.0/android")
		if hwid != "" {
			req.Header.Set("x-hwid", hwid)
		}
		body := do(req).Body.String()
		if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body)); err == nil {
			body = string(b)
		}
		m := regexp.MustCompile(`amneziawg://([A-Za-z0-9_-]+)`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no amneziawg line:\n%s", body)
		}
		conf, _ := base64.RawURLEncoding.DecodeString(strings.TrimRight(m[1], "="))
		return regexp.MustCompile(`PrivateKey = (\S+)`).FindStringSubmatch(string(conf))[1]
	}
	phone := incy("hw-phone") // claims the main key first (AddUserAWGKey)
	main := incy("")
	if phone == main {
		t.Fatal("a client with an hwid got the main key")
	}
	if incy("hw-phone") != phone {
		t.Fatal("the same client got another key on its next fetch")
	}
	keys, _ := st.ListAWGDevices(u.ID)
	if len(keys) != 1 || keys[0].HWID != "hw-phone" {
		t.Fatalf("keys after the fetch: %+v", keys)
	}

	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, base+path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-RosPanel-Sub", "1")
		return do(req)
	}
	rec := post("/awg-keys/add", map[string]string{"name": "Laptop"})
	if rec.Code != http.StatusOK {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	var added struct{ ID int64 }
	_ = json.Unmarshal(rec.Body.Bytes(), &added)

	page := func(q string) string {
		req := httptest.NewRequest(http.MethodGet, base+q, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("Accept", "text/html")
		return do(req).Body.String()
	}
	withPick := page("?k=" + jsonID(added.ID))
	if !strings.Contains(withPick, "/awg/0.conf?k="+jsonID(added.ID)) || !strings.Contains(withPick, "Laptop") {
		t.Error("the page does not write the card with the picked key")
	}
	if strings.Contains(page("?k=999999"), "?k=999999") {
		t.Error("a key the user does not hold was picked")
	}
	conf := do(httptest.NewRequest(http.MethodGet, base+"/awg/0.conf?k="+jsonID(added.ID), nil)).Body.String()
	laptop, _ := st.GetAWGDevice(u.ID, added.ID)
	if !strings.Contains(conf, "PrivateKey = "+laptop.PrivateKey) {
		t.Errorf("the .conf is not written with the picked key:\n%s", conf)
	}

	// A plain WireGuard inbound takes the same key: its card, file and QR follow the pick.
	wgPath := "/wg/" + jsonID(wg.ID) + ".conf"
	if p := page("?k=" + jsonID(added.ID)); !strings.Contains(p, wgPath+"?k="+jsonID(added.ID)) ||
		!strings.Contains(p, "pickAWGKey(") {
		t.Error("the WireGuard card does not follow the picked key")
	}
	wgConf := do(httptest.NewRequest(http.MethodGet, base+wgPath+"?k="+jsonID(added.ID), nil)).Body.String()
	if !strings.Contains(wgConf, "PrivateKey = "+laptop.PrivateKey) {
		t.Errorf("the WireGuard file is not written with the picked key:\n%s", wgConf)
	}
	if strings.Contains(do(httptest.NewRequest(http.MethodGet, base+wgPath+"?k=999999", nil)).Body.String(), "[Interface]") {
		t.Error("a WireGuard file for a key the user does not hold")
	}

	if rec := post("/awg-keys/delete", map[string]int64{"id": added.ID}); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if d, _ := st.GetAWGDevice(u.ID, added.ID); d != nil {
		t.Error("the key outlived its deletion")
	}
	// A refusal reaches the subscriber in their own language.
	if err := st.SetUserHWIDLimit(u.ID, 1); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"name": "more"})
	req := httptest.NewRequest(http.MethodPost, base+"/awg-keys/add", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-RosPanel-Sub", "1")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	rec = do(req)
	var refusal struct{ Error, Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &refusal)
	if rec.Code != http.StatusBadRequest || refusal.Code != "err.awgKeysFull" || !strings.Contains(refusal.Error, "device limit") {
		t.Errorf("past the cap: %d %s", rec.Code, rec.Body.String())
	}
	// Another user's key id is not this user's to delete.
	other, _ := mgr.CreateUser(t.Context(), "other", 0, 0)
	od, _, _ := mgr.AddUserAWGKey(t.Context(), other.ID, "theirs", "")
	if rec := post("/awg-keys/delete", map[string]int64{"id": od.ID}); rec.Code == http.StatusOK {
		t.Error("deleted another user's key")
	}
}

func jsonID(id int64) string {
	b, _ := json.Marshal(id)
	return string(b)
}
