package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A website's key ticked for POST /v1/signup registers clients under the panel's
// rules — created, then the same account again, 429 inside the minute — and cannot
// create a user on its own terms.
func TestAPISignup(t *testing.T) {
	t.Parallel()
	h, _, st := nodeAPITestServer(t)
	base, _ := apiFixture(t, h, st)
	k, err := st.CreateAPIKey("site", false, []string{model.PermUsersManage}, []string{"POST /v1/signup"})
	if err != nil {
		t.Fatal(err)
	}
	signup := func(body string) (int, apiSignupResp, string) {
		rec := apiDo(t, h, http.MethodPost, base+"/v1/signup", k.RawKey, body)
		var out struct {
			Data apiSignupResp `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Data, rec.Body.String()
	}

	if code, _, body := signup(`{"external_id":"a@example.com"}`); code != http.StatusBadRequest {
		t.Fatalf("closed registration: %d %s", code, body)
	}
	if err := st.SetTelegramUserBot(false, "", model.RegOpen, ""); err != nil {
		t.Fatal(err)
	}
	code, first, body := signup(`{"external_id":"a@example.com","ip":"203.0.113.1"}`)
	if code != http.StatusCreated || first.Status != "created" || first.User == nil || first.User.SubURL == "" {
		t.Fatalf("sign-up: %d %s", code, body)
	}
	if first.UserID != first.User.ID || first.User.ExternalID != "a@example.com" {
		t.Fatalf("created: %s", body)
	}
	// Again: the same account, named by id only — a key that may only sign up does not
	// read accounts by guessing their external ids.
	code, again, body := signup(`{"external_id":"a@example.com","ip":"203.0.113.1"}`)
	if code != http.StatusOK || again.Status != "existing" || again.User != nil || again.UserID != first.User.ID {
		t.Fatalf("again: %d %s", code, body)
	}
	rec := apiDo(t, h, http.MethodPost, base+"/v1/signup", k.RawKey, `{"external_id":"b@example.com","ip":"203.0.113.1"}`)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("same address inside the minute: %d %s", rec.Code, rec.Body.String())
	}
	if code, _, body := signup(`{}`); code != http.StatusBadRequest {
		t.Fatalf("no external_id: %d %s", code, body)
	}

	if err := st.SetTelegramUserBot(false, "", model.RegModeration, ""); err != nil {
		t.Fatal(err)
	}
	code, pending, body := signup(`{"external_id":"c@example.com"}`)
	if code != http.StatusAccepted || pending.Status != "pending" || pending.RequestID == 0 || pending.User != nil {
		t.Fatalf("moderated: %d %s", code, body)
	}

	// Found again by the site's id, with a key that may read users — and in full from
	// sign-up too.
	_, admin := apiFixture(t, h, st)
	rec = apiDo(t, h, http.MethodPost, base+"/v1/signup", admin, `{"external_id":"a@example.com"}`)
	var full struct {
		Data apiSignupResp `json:"data"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &full) != nil ||
		full.Data.User == nil || full.Data.User.ID != first.User.ID {
		t.Fatalf("existing, with users.view: %d %s", rec.Code, rec.Body.String())
	}
	rec = apiDo(t, h, http.MethodGet, base+"/v1/users?external_id=a@example.com", admin, "")
	var list struct {
		Data []userView `json:"data"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil ||
		len(list.Data) != 1 || list.Data[0].ID != first.User.ID {
		t.Fatalf("lookup: %d %s", rec.Code, rec.Body.String())
	}

	if rec := apiDo(t, h, http.MethodPost, base+"/v1/users", k.RawKey, `{"name":"free-for-all"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a sign-up key created a user directly: %d", rec.Code)
	}
}

// A client the bot signed up gets the website's id by PATCH: a later sign-up with it
// is that account again, not a second one with a second trial; an id another
// account holds is refused before anything else in the PATCH is applied.
func TestAPIPatchExternalID(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	base, key := apiFixture(t, h, st)
	botUser, _ := mgr.CreateUser(t.Context(), "from-bot", 0, 0)
	other, _ := mgr.CreateUser(t.Context(), "other", 0, 0)
	patch := func(id int64, body string) (int, string) {
		rec := apiDo(t, h, http.MethodPatch, base+"/v1/users/"+itoa64(id), key, body)
		return rec.Code, rec.Body.String()
	}
	if code, body := patch(botUser.ID, `{"external_id":" ann@example.com "}`); code != http.StatusOK ||
		!strings.Contains(body, `"external_id":"ann@example.com"`) {
		t.Fatalf("link: %d %s", code, body)
	}
	if rec := apiGet(t, h, base+"/v1/users?external_id=ann@example.com", key); !strings.Contains(rec.Body.String(), `"name":"from-bot"`) {
		t.Fatalf("lookup: %s", rec.Body.String())
	}
	if err := st.SetTelegramUserBot(false, "", model.RegOpen, ""); err != nil {
		t.Fatal(err)
	}
	rec := apiDo(t, h, http.MethodPost, base+"/v1/signup", key, `{"external_id":"ann@example.com"}`)
	if !strings.Contains(rec.Body.String(), `"status":"existing"`) || !strings.Contains(rec.Body.String(), `"user_id":`+itoa64(botUser.ID)) {
		t.Fatalf("sign-up after the link made another account: %s", rec.Body.String())
	}
	if code, body := patch(other.ID, `{"external_id":"ann@example.com","name":"renamed"}`); code != http.StatusBadRequest ||
		!strings.Contains(body, "externalIDTaken") {
		t.Fatalf("taken id: %d %s", code, body)
	}
	if u, _ := st.GetUser(other.ID); u.Name != "other" {
		t.Errorf("the refused PATCH still renamed the user to %q", u.Name)
	}
	// A PATCH refused for another field writes no id either.
	if code, body := patch(other.ID, `{"external_id":"bob@example.com","data_limit":-1}`); code != http.StatusBadRequest {
		t.Fatalf("bad data_limit: %d %s", code, body)
	}
	if got := st.UserExternalID(other.ID); got != "" {
		t.Errorf("a refused PATCH still set external_id %q", got)
	}
	if code, body := patch(botUser.ID, `{"external_id":""}`); code != http.StatusOK || strings.Contains(body, "ann@example.com") {
		t.Fatalf("clear: %d %s", code, body)
	}
	if code, body := patch(other.ID, `{"external_id":"ann@example.com"}`); code != http.StatusOK {
		t.Fatalf("a freed id is free: %d %s", code, body)
	}
}
