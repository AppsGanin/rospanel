package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The SPA shell is the right answer for a client-side route, and the wrong one for a
// request that wanted JSON. Answering an API path with a page is what produced
// "Unexpected token '<', \"<!doctype\"... is not valid JSON" in issue #70 — an error
// that names neither the request nor the cause, and which the operator who hit it took
// to be their own mistake.
func TestUnknownAPIPathAnswersJSONNotThePage(t *testing.T) {
	rt := &Router{spaIndex: []byte("<!doctype html><html><head></head></html>")}

	t.Run("an API path that no route answered", func(t *testing.T) {
		w := httptest.NewRecorder()
		rt.fallback(w, httptest.NewRequest(http.MethodGet, "/api/nodes-typo", nil))

		if w.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404 — a 200 tells the caller it worked", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type %q, want JSON", ct)
		}
		var body struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body is not JSON (%v): %s", err, w.Body.String())
		}
		if body.Code != "err.staleTab" {
			t.Errorf("code %q, want err.staleTab — the panel has to say which side is "+
				"out of date, or the next report blames the wrong one", body.Code)
		}
	})

	// Everything else still gets the shell: that is how the SPA's own routing works,
	// and a deep link the server has never heard of has to load the app.
	for _, path := range []string{"/", "/users", "/settings/subscriptions", "/apixyz"} {
		t.Run("client route "+path, func(t *testing.T) {
			w := httptest.NewRecorder()
			rt.fallback(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusOK {
				t.Errorf("status %d, want 200", w.Code)
			}
			if !strings.HasPrefix(w.Body.String(), "<!doctype") {
				t.Errorf("client route did not get the app shell: %q", w.Body.String())
			}
		})
	}
}

// A stale tab does not only GET. Before this, a PATCH to a route that had been removed
// fell through to net/http's own 405, which answers text/plain — the same failure as
// issue #70 with a different status code.
func TestUnknownPathAnswersJSONForEveryMethod(t *testing.T) {
	rt := &Router{spaIndex: []byte("<!doctype html><html><head></head></html>")}
	for _, m := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut} {
		t.Run(m, func(t *testing.T) {
			for _, path := range []string{"/api/nope", "/users"} {
				w := httptest.NewRecorder()
				rt.fallback(w, httptest.NewRequest(m, path, nil))
				if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
					t.Errorf("%s %s: Content-Type %q, want JSON", m, path, ct)
				}
				if w.Code == http.StatusOK {
					t.Errorf("%s %s: answered 200 — nothing handled this request", m, path)
				}
				// The two cases are different facts and must not share a code: only the
				// API one is "you were handed a page instead of data".
				var body struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &body)
				want := "err.methodNotSupported"
				if strings.HasPrefix(path, "/api/") {
					want = "err.staleTab"
				}
				if body.Code != want {
					t.Errorf("%s %s: code %q, want %q", m, path, body.Code, want)
				}
			}
		})
	}
	// HEAD is how a browser or a monitor asks for the page, and it must still get one.
	w := httptest.NewRecorder()
	rt.fallback(w, httptest.NewRequest(http.MethodHead, "/users", nil))
	if w.Code != http.StatusOK {
		t.Errorf("HEAD on a client route: status %d, want 200", w.Code)
	}
}
