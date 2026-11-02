package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// testPayPNG is a picture the validator accepts, in the format an operator's QR code
// screenshot comes as.
func testPayPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return b.Bytes()
}

// manualPayFixture is a payer with a paid plan and manual payment on: the state the
// picture belongs to.
func manualPayFixture(t *testing.T) (http.Handler, *model.TariffPlan, model.User) {
	t.Helper()
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "payer", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	plan := &model.TariffPlan{Name: "Paid", Slug: "paid", PriceRub: 300, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ApplyPlanToUser(t.Context(), u.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	// Re-read: applying the plan is what puts it on the user, and the page renders the
	// billing block only for a user who has one.
	if u, err = st.GetUser(u.ID); err != nil {
		t.Fatal(err)
	}
	set, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	set.BillingEnabled = true
	set.BillingManualEnabled = true
	set.BillingPaymentNote = "Transfer to card 0000"
	if err := mgr.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	return h, plan, *u
}

// The operator's picture reaches the payer where the instructions are — the order
// dialog, not the plan card: the page serves the bytes under the user's own token and
// the order response names that address, which is what the dialog loads. The card
// itself carries no picture, whatever the operator uploaded.
func TestManualPayImageReachesThePageAndTheOrder(t *testing.T) {
	t.Parallel()
	h, plan, u := manualPayFixture(t)
	rt := h.(*Router)

	// Nothing uploaded: no address on the route.
	page := fetchSubHTML(h, u.SubToken)
	if strings.Contains(page, "/payimg?v=") {
		t.Error("the page points at a picture that was never uploaded")
	}
	if rec := getSubAsset(h, u.SubToken, "payimg"); rec.Code == http.StatusOK {
		t.Errorf("an absent picture answered %d", rec.Code)
	}

	png := testPayPNG(t, 96, 96)
	if _, err := rt.mgr.SetManualPayImage(png); err != nil {
		t.Fatalf("store picture: %v", err)
	}

	// The page still names no address: the dialog takes it from the order response.
	page = fetchSubHTML(h, u.SubToken)
	if strings.Contains(page, "/payimg?v=") {
		t.Error("the plan card shows a picture; it belongs to the order instructions")
	}
	if !strings.Contains(page, `id="msgmodal-img"`) {
		t.Error("the order dialog has no place for the picture")
	}

	rec := getSubAsset(h, u.SubToken, "payimg")
	if rec.Code != http.StatusOK {
		t.Fatalf("picture route: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content type = %q, want image/png", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), png) {
		t.Error("the route served something other than the uploaded bytes")
	}

	// The manual order names the same address.
	rec = postPay(h, u.SubToken, plan.ID, sub.ManualPayKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("pay: %d %s", rec.Code, rec.Body.String())
	}
	var paid struct {
		Manual bool   `json:"manual"`
		Image  string `json:"image"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &paid); err != nil {
		t.Fatalf("answer: %s (%v)", rec.Body.String(), err)
	}
	if !paid.Manual || !strings.Contains(paid.Image, "/payimg?v=") {
		t.Errorf("order response carries no picture: %+v", paid)
	}

	// Switched off, the address is not a way around the method: it answers like any
	// other unknown path.
	set := mustSettings(t, rt)
	set.BillingManualEnabled = false
	if err := rt.mgr.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	if rec := getSubAsset(h, u.SubToken, "payimg"); rec.Header().Get("Content-Type") == "image/png" {
		t.Error("the picture is still served with manual payment off")
	}

	// Deleted: the page stops pointing at it.
	set = mustSettings(t, rt)
	set.BillingManualEnabled = true
	if err := rt.mgr.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	if err := rt.mgr.DeleteManualPayImage(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if page := fetchSubHTML(h, u.SubToken); strings.Contains(page, "/payimg?v=") {
		t.Error("the page still points at a deleted picture")
	}
}

// getSubAsset asks for a per-token file the page loads (the picture, a font, a theme).
func getSubAsset(h http.Handler, token, leaf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/sub/"+token+"/"+leaf, nil)
	req.RemoteAddr = testClientIP + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mustSettings(t *testing.T, rt *Router) *model.Settings {
	t.Helper()
	set, err := rt.mgr.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// The panel side: the upload takes a picture and nothing else, the preview serves
// what was stored, and the settings report the version the preview URL needs.
func TestManualPayImagePanelEndpoints(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	owner := signIn(t, st, "owner", model.RoleOwner, false)

	do := func(method, path string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
		var r *http.Request
		if body != nil {
			r = httptest.NewRequest(method, path, body)
			r.Header.Set("Content-Type", contentType)
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("X-RosPanel-CSRF", "1")
		r.AddCookie(owner)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	upload := func(field, name string, content []byte) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile(field, name)
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		_, _ = fw.Write(content)
		mw.Close()
		return do(http.MethodPost, "/api/billing/manual-image", &buf, mw.FormDataContentType())
	}

	// Nothing stored yet.
	if rec := do(http.MethodGet, "/api/billing/manual-image", nil, ""); rec.Code != http.StatusNotFound {
		t.Errorf("preview of an absent picture = %d, want 404", rec.Code)
	}

	// A file that is not a PNG/JPEG is refused with the code the panel translates.
	rec := upload("image", "notes.txt", []byte("not a picture"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "err.needPngJpeg") {
		t.Errorf("text upload = %d %s", rec.Code, rec.Body.String())
	}

	png := testPayPNG(t, 64, 64)
	rec = upload("image", "qr.png", png)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body.String())
	}
	var state struct {
		Image   bool   `json:"manual_image"`
		Version string `json:"manual_image_version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil || !state.Image || state.Version == "" {
		t.Fatalf("upload answer = %s (%v)", rec.Body.String(), err)
	}

	// The settings carry the same version, so the SPA's preview URL can name it.
	rec = do(http.MethodGet, "/api/billing", nil, "")
	var billing struct {
		Image   bool   `json:"manual_image"`
		Version string `json:"manual_image_version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &billing); err != nil {
		t.Fatalf("billing: %v (%s)", err, rec.Body.String())
	}
	if !billing.Image || billing.Version != state.Version {
		t.Errorf("billing says image=%v version=%q, upload said %q", billing.Image, billing.Version, state.Version)
	}

	rec = do(http.MethodGet, "/api/billing/manual-image", nil, "")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), png) {
		t.Errorf("preview = %d, %d bytes (want 200 with the uploaded bytes)", rec.Code, rec.Body.Len())
	}

	// Replacing it changes the version, so the old URL cannot win a cache.
	rec = upload("image", "qr2.png", testPayPNG(t, 32, 32))
	var replaced struct {
		Version string `json:"manual_image_version"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &replaced)
	if replaced.Version == "" || replaced.Version == state.Version {
		t.Errorf("version did not change on replace: %q → %q", state.Version, replaced.Version)
	}

	rec = do(http.MethodDelete, "/api/billing/manual-image", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/billing/manual-image", nil, ""); rec.Code != http.StatusNotFound {
		t.Errorf("preview after delete = %d, want 404", rec.Code)
	}
}

// The /v1 order response names the picture too, so an operator's own bot or site can
// show it without holding the bytes — the address is the payer's own subscription
// page, signed with their token like the page itself.
func TestAPIManualOrderCarriesThePicture(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	plan := &model.TariffPlan{Slug: "img-std", Name: "Std", PriceRub: 199, PeriodDays: 30, Enabled: true}
	if err := st.SaveTariffPlan(plan); err != nil {
		t.Fatal(err)
	}
	set, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	set.BillingEnabled, set.BillingManualEnabled = true, true
	if err := mgr.SaveBillingSettings(set); err != nil {
		t.Fatal(err)
	}
	u, err := mgr.CreateUser(t.Context(), "imgsite", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// order opens (or reuses) the manual order and returns the response data.
	order := func() map[string]any {
		t.Helper()
		rec := apiDo(t, h, http.MethodPost, base+"/v1/billing/orders", key,
			`{"user_id":`+itoa64(u.ID)+`,"plan_id":`+itoa64(plan.ID)+`,"provider":"manual"}`)
		var out struct {
			Data map[string]any `json:"data"`
		}
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("order: %d %s", rec.Code, rec.Body.String())
		}
		return out.Data
	}

	// Nothing uploaded: the field is there and empty, so a caller can branch on it.
	if got, ok := order()["image_url"]; !ok || got != "" {
		t.Fatalf("no picture uploaded, image_url = %v (present %v)", got, ok)
	}

	version, err := mgr.SetManualPayImage(testPayPNG(t, 48, 48))
	if err != nil {
		t.Fatal(err)
	}
	image, _ := order()["image_url"].(string)
	if !strings.Contains(image, "/sub/"+u.SubToken+"/payimg?v="+version) {
		t.Fatalf("image_url = %q, want the payer's own /payimg address (version %q)", image, version)
	}

}
