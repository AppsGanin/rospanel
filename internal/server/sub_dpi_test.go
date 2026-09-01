package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Xray-core apps keep getting the link list until the operator opts them into the
// JSON format; once opted in, the config they get carries the fragment/noise
// outbound, and an explicit ?format= or a response rule still overrides.
func TestXrayJSONFormatForXrayCoreClients(t *testing.T) {
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "dpi", 0, 0)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	const happ = "Happ/2.3.1 (iPhone; iOS 17.4)"

	// Default: links, as before.
	rec := fetchSubUA(h, u.SubToken, happ)
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("with the switch off Happ got %q, want the link list", ct)
	}

	dpi := model.DefaultSubDPI()
	dpi.JSONClients, dpi.Fragment = true, true
	if err := st.SetSubDPI(dpi); err != nil {
		t.Fatal(err)
	}
	rec = fetchSubUA(h, u.SubToken, happ)
	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); rec.Code != http.StatusOK || !strings.Contains(ct, "application/json") {
		t.Fatalf("Happ: %d %q", rec.Code, ct)
	}
	if !strings.HasPrefix(strings.TrimSpace(body), "[") || !strings.Contains(body, `"remarks"`) {
		t.Errorf("not an array of Xray configs:\n%s", body)
	}
	if !strings.Contains(body, `"dialerProxy": "fragment"`) || !strings.Contains(body, `"packets": "tlshello"`) {
		t.Errorf("fragment outbound missing from the Xray JSON:\n%s", body)
	}
	if !strings.Contains(body, u.UUID) {
		t.Error("the config does not carry the user's UUID")
	}
	// The Hysteria2 lane gets its own config, with no shaper in front of the QUIC
	// transport.
	if !strings.Contains(body, `"network": "hysteria"`) || !strings.Contains(body, `"hysteriaSettings"`) {
		t.Errorf("the Hysteria2 lane is missing from the Xray JSON:\n%s", body)
	}

	// Other clients are untouched.
	if ct := fetchSubUA(h, u.SubToken, "Mozilla/5.0").Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("a plain client got %q", ct)
	}
	if ct := fetchSubUA(h, u.SubToken, "sing-box/1.12").Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("sing-box got %q", ct)
	}
	// An explicit format wins over the switch.
	req := httptest.NewRequest(http.MethodGet, "/sub/"+u.SubToken+"?format=v2ray", nil)
	req.Header.Set("User-Agent", happ)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("?format=v2ray was overridden: %q", ct)
	}
	// And so does a rule.
	if err := mgr.SaveSubRules([]model.SubRule{{
		Field: model.SubMatchUserAgent, Op: model.SubOpContains, Value: "happ",
		Action: model.SubActionSingbox, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if b := fetchSubUA(h, u.SubToken, happ).Body.String(); strings.Contains(b, `"remarks"`) || !strings.Contains(b, `"outbounds"`) {
		t.Errorf("a sing-box rule should beat the auto Xray JSON:\n%s", b)
	}
	// The format is also reachable by rule for a client the auto-detect does not know.
	if err := mgr.SaveSubRules([]model.SubRule{{
		Field: model.SubMatchUserAgent, Op: model.SubOpContains, Value: "custom-app",
		Action: model.SubActionXrayJSON, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if b := fetchSubUA(h, u.SubToken, "custom-app/1").Body.String(); !strings.Contains(b, `"remarks"`) {
		t.Errorf("rule → xray-json did not serve the format:\n%s", b)
	}
}

func TestSubDPIValidation(t *testing.T) {
	ok := model.DefaultSubDPI()
	if err := ok.Validate(); err != nil {
		t.Fatalf("defaults refused: %v", err)
	}
	bad := []func(d *model.SubDPI){
		func(d *model.SubDPI) { d.FragmentPackets = "2-4" },
		func(d *model.SubDPI) { d.FragmentLength = "200-100" },
		func(d *model.SubDPI) { d.FragmentLength = "abc" },
		func(d *model.SubDPI) { d.FragmentInterval = "0-99999" },
		func(d *model.SubDPI) { d.NoiseType = "hex" },
		func(d *model.SubDPI) { d.NoiseType = "rand"; d.NoisePacket = "0" },
		func(d *model.SubDPI) { d.NoiseType = "base64"; d.NoisePacket = "not base64!" },
		func(d *model.SubDPI) { d.NoiseType = "str"; d.NoisePacket = strings.Repeat("x", 300) },
		func(d *model.SubDPI) { d.NoiseDelay = "-5" },
	}
	for i, mutate := range bad {
		d := model.DefaultSubDPI()
		mutate(&d)
		if err := d.Validate(); err == nil {
			t.Errorf("case %d accepted: %+v", i, d)
		}
	}
	// Blank fields fall back to the defaults instead of failing.
	d := model.SubDPI{Fragment: true}
	if err := d.Validate(); err != nil {
		t.Errorf("blank fields should normalise to defaults: %v", err)
	}
	if n := d.Normalized(); n.FragmentLength != "100-200" || n.NoiseType != "rand" {
		t.Errorf("normalised: %+v", n)
	}
}
