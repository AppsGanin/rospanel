package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/decoy"
)

// When the per-user access read fails the panel refuses to build a subscription: serving
// one would mean guessing at what the user may reach, and guessing "everything" hands a
// restricted user the address of every lane on every server.
//
// The refusal must be a NON-2xx with nothing parseable in it. A client that gets a 2xx
// treats the fetch as successful, and one that replaces its profile on success would wipe
// the user's servers over an answer that only means "ask again later".
func TestSubUnavailableIsAFailedFetchNotAnEmptyConfig(t *testing.T) {
	rt := &Router{}
	rec := httptest.NewRecorder()
	rt.subUnavailable(rec, 7, errors.New("database is closed"))

	if rec.Code/100 == 2 {
		t.Errorf("answered %d — a client reads that as a successful refresh", rec.Code)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503: this is a host having a bad moment, not a missing page", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body is %d bytes, want empty: anything parseable invites a client to treat it as a config", rec.Body.Len())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After — the condition is temporary and the client should be told so")
	}
}

// Why subUnavailable exists rather than falling through to the decoy, pinned so nobody
// "simplifies" it back: a subscription URL is extensionless, and for that the decoy
// deliberately answers 200 with its index page — imitating the `try_files $uri /index.html`
// hosting it masquerades as. Nine of the twelve bundled templates ship no 404.html at all,
// so on those installs a decoy fallback would answer a subscription refresh with a
// successful HTML page.
func TestDecoyAnswersAnExtensionlessMissWith200(t *testing.T) {
	names, err := decoy.Available()
	if err != nil || len(names) == 0 {
		t.Fatalf("decoy templates: %v", err)
	}
	var checked int
	for _, name := range names {
		h, err := decoy.New(name, decoy.LoadStamp(t.TempDir()))
		if err != nil {
			continue
		}
		req := httptest.NewRequest(http.MethodGet, "/sub/some-token", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			checked++
			if !strings.Contains(strings.ToLower(rec.Body.String()), "<html") {
				t.Errorf("%s: 200 without an HTML body — the premise changed", name)
			}
		}
	}
	if checked == 0 {
		t.Error("no template answers an extensionless miss with 200 any more — " +
			"if that is deliberate, subUnavailable's reasoning needs revisiting")
	}
	t.Logf("%d of %d templates answer an extensionless miss with 200", checked, len(names))
}
