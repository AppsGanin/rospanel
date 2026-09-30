package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A website's key holds users.signup: it registers clients under the panel's rules —
// created, then the same account again, 429 inside the minute — and cannot create a
// user on its own terms.
func TestAPISignup(t *testing.T) {
	t.Parallel()
	h, _, st := nodeAPITestServer(t)
	base, _ := apiFixture(t, h, st)
	role, err := st.CreateAdminRole("site", []string{model.PermUsersSignup})
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.CreateAPIKey("site", role.Key)
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

// Whoever may create users on any terms may sign them up under the rules as well.
func TestUsersManageImpliesSignup(t *testing.T) {
	t.Parallel()
	got := model.NormalizePerms([]string{model.PermUsersManage})
	for _, p := range got {
		if p == model.PermUsersSignup {
			return
		}
	}
	t.Fatalf("users.manage normalises to %v, without users.signup", got)
}
