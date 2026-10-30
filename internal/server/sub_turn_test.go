package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A WireGuard inbound made through the manager gets its key and loopback port from the
// panel, keeps the private key off every view, and reaches the user as a card on the
// subscription page — the relay's address, the call link, a command line — and a plain
// WireGuard config pointed at the TURN client on their device.
func TestTurnWireGuardEndpointsAndPageCard(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "turn", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := st.GetSettings()
	if err := st.SetTLSMode(cur.TLSMode, "vpn.example.com", "vpn.example.com", cur.ACMEEmail); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()

	pc, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	pc.Close()
	view, err := mgr.CreateInbound(t.Context(), model.Inbound{
		ServerID: model.LocalNodeID, Enabled: true, Name: "Calls", Protocol: model.InbTurnVK, Port: port,
		Opts: model.InboundOpts{TurnLink: "https://vk.com/call/join/first"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.Opts.WGPrivateKey != "" || view.Opts.TurnMaskKey != "" {
		t.Error("the view carries the inbound's private or masking key")
	}
	if view.Opts.WGPublicKey == "" || view.Opts.WGLocalPort == 0 || view.Opts.WGLocalPort == port {
		t.Fatalf("generated material: pub=%q local=%d", view.Opts.WGPublicKey, view.Opts.WGLocalPort)
	}
	stored, _ := st.GetInbound(view.ID)
	if stored.Opts.WGPrivateKey == "" || len(stored.Opts.TurnMaskKey) != 64 {
		t.Fatal("the private or masking key was not stored")
	}

	// An edit keeps the identity and the loopback port.
	edit := *stored
	edit.Opts = model.InboundOpts{TurnLink: "https://vk.com/call/join/xyz"}
	edited, err := mgr.UpdateInbound(t.Context(), edit)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if edited.Opts.WGPublicKey != view.Opts.WGPublicKey || edited.Opts.WGLocalPort != view.Opts.WGLocalPort {
		t.Errorf("an edit changed the identity or port: %+v", edited.Opts)
	}
	if again, _ := st.GetInbound(view.ID); again.Opts.TurnMaskKey != stored.Opts.TurnMaskKey {
		t.Error("an edit changed the masking key, which every imported client holds")
	}
	if edited.Opts.TurnLink != "https://vk.com/call/join/xyz" {
		t.Errorf("turn link = %q", edited.Opts.TurnLink)
	}

	get := func(path, ua string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", ua)
		if strings.HasPrefix(ua, "Mozilla") {
			req.Header.Set("Accept", "text/html,application/xhtml+xml")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	base := "/sub/" + u.SubToken
	id := strconv.FormatInt(view.ID, 10)
	peer := "vpn.example.com:" + strconv.Itoa(port)

	page := get(base, "Mozilla/5.0").Body.String()
	// The VK Turn Proxy link, as a tappable href whose scheme html/template kept — the
	// user has never downloaded a config, so the page itself had to claim their key.
	for _, scheme := range []string{"vkturnproxy://import?data=", "freeturn://", "wingsv://"} {
		if !strings.Contains(page, `href="`+scheme) {
			t.Errorf("page lacks a working %s link", scheme)
		}
	}
	if strings.Contains(page, "ZgotmplZ") {
		t.Error("html/template filtered an app link's scheme")
	}
	// Each app says where it comes from: a link handed to a user is an app to install.
	for _, repo := range []string{
		"https://github.com/anton48/vk-turn-proxy-ios",
		"https://github.com/samosvalishe/turn-proxy-android",
		"https://github.com/WINGS-N/WINGSV",
	} {
		if !strings.Contains(page, `href="`+repo+`"`) {
			t.Errorf("page lacks the source link %s", repo)
		}
	}
	if strings.Contains(page, "/wg/"+id+".png") {
		t.Error("page still offers a QR for the lane")
	}
	for _, want := range []string{"/wg/" + id + ".conf", peer, "https://vk.com/call/join/xyz"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}

	conf := get(base+"/wg/"+id+".conf", "curl/8")
	body := conf.Body.String()
	if conf.Code != http.StatusOK {
		t.Fatalf("config: %d %s", conf.Code, body)
	}
	for _, want := range []string{"[Interface]", "MTU = 1280", "PublicKey = " + view.Opts.WGPublicKey, "Endpoint = 127.0.0.1:9000"} {
		if !strings.Contains(body, want) {
			t.Errorf("config lacks %q:\n%s", want, body)
		}
	}
	// Plain WireGuard: its own apps refuse a file with AmneziaWG's keys in it.
	for _, not := range []string{"Jc = ", "H1 = ", "S1 = "} {
		if strings.Contains(body, not) {
			t.Errorf("config carries AmneziaWG key %q:\n%s", not, body)
		}
	}
	if cd := conf.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="Calls.conf"`) {
		t.Errorf("file name: %q", cd)
	}
	// Not a wireguard inbound, not a number, not the file: the decoy.
	for _, path := range []string{"/wg/999.conf", "/wg/x.conf", "/wg/" + id + ".txt", "/wg/" + id + ".png"} {
		if rec := get(base+path, "curl/8"); strings.Contains(rec.Body.String(), "[Interface]") || rec.Header().Get("Content-Type") == "image/png" {
			t.Errorf("%s served a config", path)
		}
	}

	// The user card lists the config address as the lane's link.
	fresh, _ := st.GetUser(u.ID)
	custom, _ := st.EnabledInbounds(model.LocalNodeID)
	uv := makeUserView(*fresh, set, "", custom, nil, model.UnrestrictedAccess())
	found := false
	for _, l := range uv.Links {
		if l.Name == "Calls" && strings.HasSuffix(l.URL, "/wg/"+id+".conf") {
			found = true
		}
	}
	if !found {
		t.Errorf("user view lacks the WireGuard link: %+v", uv.Links)
	}
}

// A plain WireGuard inbound made through the manager gets its key from the panel and no
// loopback port, keeps the private key off every view, reaches the user as a card on the
// subscription page with a QR and a file pointed at the server itself — and is in no
// other format of the subscription.
func TestPlainWireGuardEndpointsAndPageCard(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "plainwg", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := st.GetSettings()
	if err := st.SetTLSMode(cur.TLSMode, "vpn.example.com", "vpn.example.com", cur.ACMEEmail); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()

	pc, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	pc.Close()
	view, err := mgr.CreateInbound(t.Context(), model.Inbound{
		ServerID: model.LocalNodeID, Enabled: true, Name: "Plain WG", Protocol: model.InbWireGuard, Port: port,
		// What a hostile or sloppy API client might add: none of it may survive.
		Opts: model.InboundOpts{TurnLink: "https://vk.com/call/join/x", WGLocalPort: 20001},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.Opts.WGPrivateKey != "" || view.Opts.TurnMaskKey != "" {
		t.Error("the view carries the inbound's private or masking key")
	}
	if view.Opts.WGPublicKey == "" || view.Opts.WGLocalPort != 0 || view.Opts.TurnLink != "" || view.Opts.CDNHost != "" {
		t.Fatalf("generated or leftover material: %+v", view.Opts)
	}
	stored, _ := st.GetInbound(view.ID)
	if stored.Opts.WGPrivateKey == "" || stored.Opts.TurnMaskKey != "" {
		t.Fatal("the private key was not stored, or a masking key was")
	}
	// An edit keeps the identity.
	edit := *stored
	edit.Opts = model.InboundOpts{}
	edited, err := mgr.UpdateInbound(t.Context(), edit)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if edited.Opts.WGPublicKey != view.Opts.WGPublicKey {
		t.Error("an edit changed the server key, stranding every imported config")
	}

	get := func(path, ua string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", ua)
		if strings.HasPrefix(ua, "Mozilla") {
			req.Header.Set("Accept", "text/html,application/xhtml+xml")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	base := "/sub/" + u.SubToken
	id := strconv.FormatInt(view.ID, 10)

	page := get(base, "Mozilla/5.0").Body.String()
	for _, want := range []string{"/wg/" + id + ".conf", "/wg/" + id + ".png", "wireguard.com/install"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, not := range []string{"vkturnproxy://", "freeturn://", "wingsv://"} {
		if strings.Contains(page, not) {
			t.Errorf("page offers a TURN app link %q for a plain lane", not)
		}
	}

	conf := get(base+"/wg/"+id+".conf", "curl/8")
	body := conf.Body.String()
	if conf.Code != http.StatusOK {
		t.Fatalf("config: %d %s", conf.Code, body)
	}
	for _, want := range []string{"[Interface]", "MTU = 1420", "PublicKey = " + view.Opts.WGPublicKey,
		"Endpoint = vpn.example.com:" + strconv.Itoa(port)} {
		if !strings.Contains(body, want) {
			t.Errorf("config lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "127.0.0.1:9000") || strings.Contains(body, "cdn.example.com") {
		t.Errorf("config points somewhere other than the server:\n%s", body)
	}
	if cd := conf.Header().Get("Content-Disposition"); !strings.Contains(cd, ".conf") || conf.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("headers: %q / %q", cd, conf.Header().Get("Cache-Control"))
	}
	if png := get(base+"/wg/"+id+".png", "curl/8"); png.Code != http.StatusOK || png.Header().Get("Content-Type") != "image/png" {
		t.Errorf("QR: %d %s", png.Code, png.Header().Get("Content-Type"))
	}
	// Not an inbound, not a number, not a file: the decoy.
	for _, path := range []string{"/wg/999.conf", "/wg/x.conf", "/wg/" + id + ".txt"} {
		if rec := get(base+path, "curl/8"); strings.Contains(rec.Body.String(), "[Interface]") || rec.Header().Get("Content-Type") == "image/png" {
			t.Errorf("%s served a config", path)
		}
	}
	// Somebody else's token gets nothing of this user's config.
	if rec := get("/sub/not-a-token/wg/"+id+".conf", "curl/8"); strings.Contains(rec.Body.String(), "[Interface]") {
		t.Error("an unknown token got a config")
	}

	// In no format of the subscription: not as a link, a Clash proxy, a sing-box outbound
	// or an Xray JSON one.
	for _, q := range []struct{ path, ua string }{
		{base, "Happ/1.0"}, {base + "?format=clash", "clash.meta"}, {base + "?format=singbox", "sing-box"},
		{base + "?format=xray-json", "Happ/1.0"},
	} {
		rec := get(q.path, q.ua)
		if strings.Contains(rec.Body.String(), "Plain WG") || strings.Contains(rec.Body.String(), strconv.Itoa(port)) {
			t.Errorf("%s carries the plain WireGuard inbound:\n%.600s", q.path, rec.Body.String())
		}
	}

	fresh, _ := st.GetUser(u.ID)
	custom, _ := st.EnabledInbounds(model.LocalNodeID)
	uv := makeUserView(*fresh, set, "", custom, nil, model.UnrestrictedAccess())
	found := false
	for _, l := range uv.Links {
		if l.Name == "Plain WG" && strings.HasSuffix(l.URL, "/wg/"+id+".conf") {
			found = true
		}
	}
	if !found {
		t.Errorf("user view lacks the WireGuard link: %+v", uv.Links)
	}
	closed := makeUserView(*fresh, set, "", custom, nil, model.Access{Tokens: map[string]bool{}})
	for _, l := range closed.Links {
		if l.Name == "Plain WG" {
			t.Errorf("a user the inbound is closed to sees it: %+v", l)
		}
	}

	// And over HTTP: a member of a group that limits access to the built-in VLESS lane
	// gets neither the file, the QR nor the card.
	g, err := mgr.CreateGroup("novpn", []string{model.BuiltinToken(model.LocalNodeID, model.LaneVLESS)}, 0)
	if err != nil {
		t.Fatalf("group: %v", err)
	}
	if !g.LimitsAccess {
		t.Skip("a group with a grant does not limit access in this build")
	}
	if err := mgr.SetUserGroups(u.ID, []int64{g.ID}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	for _, path := range []string{base + "/wg/" + id + ".conf", base + "/wg/" + id + ".png"} {
		if rec := get(path, "curl/8"); strings.Contains(rec.Body.String(), "[Interface]") || rec.Header().Get("Content-Type") == "image/png" {
			t.Errorf("%s served to a user the inbound is closed to", path)
		}
	}
	if page := get(base, "Mozilla/5.0").Body.String(); strings.Contains(page, "/wg/"+id+".conf") {
		t.Error("the page offers a closed inbound")
	}
}
