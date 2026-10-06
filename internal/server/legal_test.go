package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The documents are served at their random address, linked from the subscription
// page and its API view, and handed out by GET /v1/legal; an empty or unknown one
// is the decoy like any missing page.
func TestLegalDocuments(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	u, err := mgr.CreateUser(t.Context(), "legal-user", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ExecForTest(`UPDATE settings SET host = 'vpn.example.com' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveLegalDocs(map[string]string{model.LegalTerms: "# Terms\n\nBe **nice**."}); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	if set.LegalPath == "" {
		t.Fatal("no address for the documents after saving one")
	}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	page := get("/sub/" + set.LegalPath + "/terms")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "<strong>nice</strong>") {
		t.Fatalf("terms page: %d %.200s", page.Code, page.Body.String())
	}
	for _, p := range []string{"/sub/" + set.LegalPath + "/privacy", "/sub/" + set.LegalPath + "/other", "/sub/" + set.LegalPath} {
		if rec := get(p); strings.Contains(rec.Body.String(), "Политика") || strings.Contains(rec.Body.String(), "Privacy policy") {
			t.Errorf("%s answered as a document", p)
		}
	}

	// The subscription page links the one that has text, at its foot.
	sp := get("/sub/" + u.SubToken).Body.String()
	if !strings.Contains(sp, "/sub/"+set.LegalPath+"/terms") || strings.Contains(sp, "/sub/"+set.LegalPath+"/privacy") {
		t.Error("the subscription page's document links are wrong")
	}
	var view struct {
		Data struct {
			TermsURL   string `json:"terms_url"`
			PrivacyURL string `json:"privacy_url"`
		} `json:"data"`
	}
	rec := apiGet(t, h, base+"/v1/users/"+itoa64(u.ID)+"/subscription", key)
	if json.Unmarshal(rec.Body.Bytes(), &view) != nil || !strings.HasSuffix(view.Data.TermsURL, "/terms") || view.Data.PrivacyURL != "" {
		t.Errorf("view links: %+v", view.Data)
	}
	var legal struct {
		Data apiLegalResp `json:"data"`
	}
	rec = apiGet(t, h, base+"/v1/legal", key)
	if json.Unmarshal(rec.Body.Bytes(), &legal) != nil || legal.Data.Terms.Markdown == "" ||
		!strings.Contains(legal.Data.Terms.HTML, "<h1") || legal.Data.Terms.UpdatedAt == 0 || legal.Data.Privacy.URL != "" {
		t.Fatalf("GET /v1/legal: %s", rec.Body.String())
	}

	// Re-saving the same text keeps its date; a document past the bound is refused.
	before := legal.Data.Terms.UpdatedAt
	if err := mgr.SaveLegalDocs(map[string]string{model.LegalTerms: "# Terms\n\nBe **nice**."}); err != nil {
		t.Fatal(err)
	}
	if docs, _ := mgr.LegalDocs(); docs[model.LegalTerms].UpdatedAt != before {
		t.Error("re-saving the same text moved its date")
	}
	if err := mgr.SaveLegalDocs(map[string]string{model.LegalTerms: strings.Repeat("x", model.MaxLegalDocLen+1)}); err == nil {
		t.Error("a document past the bound was stored")
	}
}

// A long agreement in Cyrillic — two bytes a character — saves: the body limit is
// the documents' own, and the length check that refuses is the one with its own
// message, not a cut-off body.
func TestLegalSaveTakesLongDocuments(t *testing.T) {
	t.Parallel()
	rt, st := rolesTestRouter(t)
	h := rt.panelMux()
	admin := signIn(t, st, "legal-admin", model.RoleAdmin, false)
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/settings/legal", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-RosPanel-CSRF", "1")
		req.AddCookie(admin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	long, _ := json.Marshal(map[string]string{"terms": strings.Repeat("Соглашение ", 15000)}) // 165k characters, ~300 KB
	if rec := post(string(long)); rec.Code != http.StatusOK {
		t.Fatalf("a 165k-character document: %d %.200s", rec.Code, rec.Body.String())
	}
	tooLong, _ := json.Marshal(map[string]string{"terms": strings.Repeat("я", model.MaxLegalDocLen+1)})
	if rec := post(string(tooLong)); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "legalTooLong") {
		t.Fatalf("past the bound: %d %.200s", rec.Code, rec.Body.String())
	}
}
