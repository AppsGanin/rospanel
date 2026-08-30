package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An unknown API path must answer JSON, not the SPA shell.
//
// The router used to answer every non-asset path with the app shell (issue #70). When
// an old browser tab still called an endpoint that was renamed or removed, the API
// client parsed "<!doctype" and failed with "not valid JSON" — which pointed at
// nothing and gave the operator no idea what was broken. A 404 with a code says which
// side is out of date.
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
		if !strings.Contains(w.Body.String(), `"code":"err.staleTab"`) {
			t.Errorf("expected err.staleTab in body, got %q", w.Body.String())
		}
	})

	t.Run("an API path with a subpath that does not exist", func(t *testing.T) {
		w := httptest.NewRecorder()
		rt.fallback(w, httptest.NewRequest(http.MethodGet, "/api/users/not-a-subpath", nil))

		if w.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type %q, want JSON", ct)
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
