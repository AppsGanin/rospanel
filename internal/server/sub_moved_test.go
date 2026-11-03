package server

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A move template goes out to every client as moved-permanently-to, with the user's
// own token filled in; an empty setting sends nothing at all.
func TestSubMovedHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	setSubHeaders(rec, model.User{SubToken: "tok123"}, &model.Settings{
		SubMovedTo: "https://new.example.com/sub/{token}",
	}, "")
	if got := rec.Header().Get("moved-permanently-to"); got != "https://new.example.com/sub/tok123" {
		t.Fatalf("moved-permanently-to = %q", got)
	}

	rec = httptest.NewRecorder()
	setSubHeaders(rec, model.User{SubToken: "tok123"}, &model.Settings{}, "")
	if got := rec.Header().Get("moved-permanently-to"); got != "" {
		t.Fatalf("empty setting sent %q", got)
	}
}

// The operator sets the move in the panel, it survives the round trip, and the
// subscription a client fetches afterwards carries it — while a value that is not an
// absolute http(s) address is refused and leaves the saved one alone.
func TestSubMovedToSetting(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)
	u, err := mgr.CreateUser(t.Context(), "mover", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cookie := signIn(t, st, "owner", model.RoleOwner, false)
	// The panel lives under the secret segment, and a POST there has to carry the CSRF
	// header the app itself sends. The form posts the whole settings object, so the test
	// starts from what the panel reports and changes that one field.
	req := httptest.NewRequest(http.MethodGet, "/secretpath/api/settings", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body.String())
	}
	var form map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &form); err != nil {
		t.Fatalf("settings: %v", err)
	}
	save := func(moved string) (int, string) {
		body := maps.Clone(form)
		body["sub_moved_to"] = moved
		enc, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/secretpath/api/settings/subscription", bytes.NewReader(enc))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-RosPanel-CSRF", "1")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	fetch := func() string {
		req := httptest.NewRequest(http.MethodGet, "/sub/"+u.SubToken, nil)
		req.Header.Set("User-Agent", "v2rayNG/1.8.0")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header().Get("moved-permanently-to")
	}

	if got := fetch(); got != "" {
		t.Fatalf("nothing saved, yet the header is %q", got)
	}
	if code, body := save("new.example.com/sub"); code != http.StatusBadRequest {
		t.Fatalf("a host without a scheme was accepted: %d %s", code, body)
	}
	if code, body := save("https://new.example.com/sub/{token}"); code != http.StatusOK {
		t.Fatalf("save: %d %s", code, body)
	}
	if set, _ := st.GetSettings(); set.SubMovedTo != "https://new.example.com/sub/{token}" {
		t.Fatalf("saved %q, want the template", set.SubMovedTo)
	}
	if got, want := fetch(), "https://new.example.com/sub/"+u.SubToken; got != want {
		t.Fatalf("header %q, want %q", got, want)
	}
	// Clearing it stops the header: the panel is the new home already.
	if code, body := save(""); code != http.StatusOK {
		t.Fatalf("clear: %d %s", code, body)
	}
	if got := fetch(); got != "" {
		t.Fatalf("cleared, yet the header is %q", got)
	}
}
